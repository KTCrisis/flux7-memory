package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/KTCrisis/mem7/internal/memory"
	"github.com/KTCrisis/mem7/internal/transport"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "mem7:", err)
		os.Exit(1)
	}
}

// run is the top-level command dispatcher. Without arguments (or with
// unrecognised ones), mem7 runs as an MCP stdio server ; subcommands
// are `serve` (HTTP backend), `rescan` (rebuild the SQLite index from
// the markdown workspace) and `prune` (drop TTL-expired entries).
func run(args []string) error {
	if len(args) == 0 {
		return runStdio()
	}
	switch args[0] {
	case "serve":
		return runServe(args[1:])
	case "rescan":
		return runRescan(args[1:])
	case "prune":
		return runPrune(args[1:])
	default:
		return fmt.Errorf("unknown subcommand %q (valid: serve, rescan, prune)", args[0])
	}
}

func dataDir() string {
	if dir := os.Getenv("MEM7_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".mem7")
}

func maxEntriesFromEnv() int {
	if v := os.Getenv("MEM7_MAX_ENTRIES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 10000
}

func newStore() (*memory.Store, error) {
	s, err := memory.NewStore(dataDir(), maxEntriesFromEnv())
	if err != nil {
		return nil, err
	}
	if url := os.Getenv("MEM7_EMBED_URL"); url != "" {
		model := os.Getenv("MEM7_EMBED_MODEL")
		if model == "" {
			model = "nomic-embed-text"
		}
		provider := os.Getenv("MEM7_EMBED_PROVIDER")
		key := os.Getenv("MEM7_EMBED_KEY")
		s.SetEmbedder(url, model, provider, key)
	}
	if url := os.Getenv("MEM7_RERANK_URL"); url != "" {
		model := os.Getenv("MEM7_RERANK_MODEL")
		if model == "" {
			model = "gemma4:e4b"
		}
		s.SetReranker(url, model)
	}
	return s, nil
}

func newDispatcher(store *memory.Store) *memory.Dispatcher {
	return memory.NewDispatcher(store)
}

// --- stdio mode ---

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func runStdio() error {
	addr := envOr("MEM7_LISTEN", "localhost:9070")
	if daemonRunning(addr) {
		token := os.Getenv("MEM7_TOKEN")
		return runStdioProxy(addr, token)
	}

	store, err := newStore()
	if err != nil {
		return err
	}
	defer store.Close()
	t := transport.NewLocal(newDispatcher(store))
	return serveStdio(context.Background(), t, os.Stdin, os.Stdout)
}

// serveStdio reads JSON-RPC requests line by line and writes responses
// line by line, routing every call through the supplied Transport.
func serveStdio(ctx context.Context, t transport.Transport, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)
	writer := bufio.NewWriter(out)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		// Skip notifications (no ID).
		if len(req.ID) == 0 {
			continue
		}

		resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
		result, err := t.Call(ctx, req.Method, req.Params)
		if err != nil {
			code := -32603
			msg := err.Error()
			var rerr *memory.RPCError
			if errors.As(err, &rerr) {
				code = rerr.Code
				msg = rerr.Message
			}
			resp.Error = &rpcError{Code: code, Message: msg}
		} else {
			resp.Result = result
		}

		data, _ := json.Marshal(resp)
		_, _ = writer.Write(data)
		_ = writer.WriteByte('\n')
		_ = writer.Flush()
	}
	return scanner.Err()
}

// --- serve mode ---

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := fs.String("listen", envOr("MEM7_LISTEN", ":9070"), "address to listen on")
	token := fs.String("token", os.Getenv("MEM7_TOKEN"), "bearer token for authentication (empty = disabled, logs a warning)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	logger := log.New(os.Stderr, "mem7 ", log.LstdFlags|log.Lmsgprefix)
	store, err := newStore()
	if err != nil {
		return err
	}
	defer store.Close()
	t := transport.NewLocal(newDispatcher(store))
	server := transport.NewHTTPServer(t, *token, logger)

	if *token == "" {
		logger.Println("WARNING: serving without authentication (no --token / MEM7_TOKEN set)")
	}
	logger.Printf("listening on %s (data dir %s)", *listen, dataDir())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return server.ListenAndServe(ctx, *listen)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// --- rescan mode ---

func runRescan(args []string) error {
	fs := flag.NewFlagSet("rescan", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	logger := log.New(os.Stderr, "mem7 ", log.LstdFlags|log.Lmsgprefix)
	store, err := newStore()
	if err != nil {
		return err
	}
	defer store.Close()

	logger.Printf("rescanning markdown workspace at %s ...", dataDir())
	n, err := store.Rescan()
	if err != nil {
		return err
	}
	logger.Printf("rescan complete, %d live entries in index", n)
	return nil
}

// --- prune mode ---

func runPrune(args []string) error {
	fs := flag.NewFlagSet("prune", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	logger := log.New(os.Stderr, "mem7 ", log.LstdFlags|log.Lmsgprefix)
	store, err := newStore()
	if err != nil {
		return err
	}
	defer store.Close()

	n, err := store.Prune()
	if err != nil {
		return err
	}
	logger.Printf("pruned %d TTL-expired entries from index", n)
	return nil
}
