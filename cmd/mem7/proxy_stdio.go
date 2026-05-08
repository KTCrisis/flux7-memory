package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

func daemonRunning(addr string) bool {
	host := normalizeAddr(addr)
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(fmt.Sprintf("http://%s/healthz", host))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

func runStdioProxy(addr, token string) error {
	log.Printf("daemon detected at %s — proxying stdio to HTTP", normalizeAddr(addr))
	return runStdioProxyWith(addr, token, os.Stdin, os.Stdout)
}

func runStdioProxyWith(addr, token string, in io.Reader, out io.Writer) error {
	host := normalizeAddr(addr)
	base := fmt.Sprintf("http://%s", host)
	client := &http.Client{Timeout: 30 * time.Second}

	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4<<20), 4<<20)
	writer := bufio.NewWriter(out)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var peek struct {
			ID json.RawMessage `json:"id,omitempty"`
		}
		_ = json.Unmarshal(line, &peek)
		if len(peek.ID) == 0 {
			continue
		}

		req, err := http.NewRequest("POST", base+"/rpc", bytes.NewReader(line))
		if err != nil {
			return fmt.Errorf("proxy: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("proxy: daemon unreachable: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("proxy: read response: %w", err)
		}

		_, _ = writer.Write(bytes.TrimRight(body, "\r\n"))
		_ = writer.WriteByte('\n')
		_ = writer.Flush()
	}
	return scanner.Err()
}

func normalizeAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "localhost" + addr
	}
	return addr
}
