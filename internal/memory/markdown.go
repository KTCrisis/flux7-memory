package memory

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Markdown workspace layout under <dir>:
//
//   workspace/
//     MEMORY.md                      (reserved for Phase 1.4 long-term notes)
//     memory/
//       YYYY-MM-DD.md                (append-only daily log)
//
// Each entry is a level-2 heading followed by a fenced YAML block
// (the "mem7 envelope") and a free-form markdown body, terminated
// by a horizontal rule. The envelope is parsed by hand — no YAML
// library is used. Only the fields mem7 writes are recognised ;
// unknown fields round-trip through rescan untouched.

const (
	workspaceDir = "workspace"
	memoryDir    = "memory"
	envelopeOpen = "```mem7"
	envelopeEnd  = "```"
	entrySep     = "---"
)

// markdownWriter appends entries to the daily log file. It must be
// called under the Store's mutex — it is not goroutine-safe on its own.
type markdownWriter struct {
	root string
	// head is the hash of the last sealed entry (chain.go); key, when set,
	// makes the seal an HMAC that only its holder can forge.
	head string
	key  []byte
}

func newMarkdownWriter(root string) *markdownWriter {
	return &markdownWriter{root: root}
}

// AppendStore writes a store-operation entry to today's daily log.
// Returns the path of the file it wrote to and the line number of the
// heading, so the SQLite index can keep a back-reference.
func (w *markdownWriter) AppendStore(f fact) (path string, line int, err error) {
	return w.appendSealed(f.Updated, formatStoreEntry(f))
}

// AppendDelete writes a delete-by-entity tombstone.
func (w *markdownWriter) AppendDelete(entity, agent, traceID string, when time.Time) error {
	_, _, err := w.appendSealed(when, formatDeleteEntry(entity, agent, traceID, when))
	return err
}

// AppendDeleteTags writes a delete-by-tags tombstone.
func (w *markdownWriter) AppendDeleteTags(tags []string, agent, traceID string, when time.Time) error {
	_, _, err := w.appendSealed(when, formatDeleteTagsEntry(tags, agent, traceID, when))
	return err
}

func (w *markdownWriter) dailyPath(when time.Time) string {
	day := when.UTC().Format("2006-01-02")
	return filepath.Join(w.root, workspaceDir, memoryDir, day+".md")
}

func (w *markdownWriter) appendToDaily(when time.Time, block string) (string, int, error) {
	path := w.dailyPath(when)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", 0, err
	}

	// Count existing lines to report the heading line number.
	existing := 0
	if data, err := os.ReadFile(path); err == nil {
		existing = strings.Count(string(data), "\n")
		if len(data) > 0 && data[len(data)-1] != '\n' {
			existing++
		}
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	if _, err := f.WriteString(block); err != nil {
		return "", 0, err
	}
	// Heading is the first line of the block ; skip the leading
	// blank line if the file was non-empty (the block starts with "\n").
	line := existing + 1
	if existing > 0 && strings.HasPrefix(block, "\n") {
		line = existing + 2
	}
	return path, line, nil
}

func formatStoreEntry(f fact) string {
	var sb strings.Builder
	sb.WriteString("\n## ")
	sb.WriteString(f.Entity)
	sb.WriteString("\n\n")
	sb.WriteString(envelopeOpen)
	sb.WriteByte('\n')
	sb.WriteString("op: store\n")
	if f.Predicate != "" && f.Predicate != defaultPredicate {
		sb.WriteString("predicate: ")
		sb.WriteString(f.Predicate)
		sb.WriteByte('\n')
	}
	if f.Agent != "" {
		sb.WriteString("agent: ")
		sb.WriteString(f.Agent)
		sb.WriteByte('\n')
	}
	if f.TraceID != "" {
		sb.WriteString("trace: ")
		sb.WriteString(f.TraceID)
		sb.WriteByte('\n')
	}
	if len(f.Tags) > 0 {
		sb.WriteString("tags: ")
		sb.WriteString(strings.Join(f.Tags, ", "))
		sb.WriteByte('\n')
	}
	if f.TTL > 0 {
		fmt.Fprintf(&sb, "ttl: %d\n", f.TTL)
	}
	sb.WriteString("created: ")
	sb.WriteString(f.Created.UTC().Format(time.RFC3339))
	sb.WriteByte('\n')
	sb.WriteString("updated: ")
	sb.WriteString(f.Updated.UTC().Format(time.RFC3339))
	sb.WriteByte('\n')
	sb.WriteString(envelopeEnd)
	sb.WriteString("\n\n")
	sb.WriteString(escapeBody(f.Object))
	if !strings.HasSuffix(f.Object, "\n") {
		sb.WriteByte('\n')
	}
	sb.WriteByte('\n')
	sb.WriteString(entrySep)
	sb.WriteByte('\n')
	return sb.String()
}

// escapeBody prefixes lines in the body that would be mis-parsed as
// structural markers (entry separator, envelope open/close) with a
// backslash. The parser ignores escaped lines because they no longer
// match the exact patterns it looks for.
func escapeBody(body string) string {
	lines := strings.Split(body, "\n")
	changed := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == entrySep || trimmed == envelopeOpen || trimmed == envelopeEnd {
			lines[i] = `\` + line
			changed = true
		}
	}
	if !changed {
		return body
	}
	return strings.Join(lines, "\n")
}

func formatDeleteEntry(entity, agent, traceID string, when time.Time) string {
	var sb strings.Builder
	sb.WriteString("\n## ")
	sb.WriteString(entity)
	sb.WriteString("\n\n")
	sb.WriteString(envelopeOpen)
	sb.WriteByte('\n')
	sb.WriteString("op: delete\n")
	if agent != "" {
		sb.WriteString("agent: ")
		sb.WriteString(agent)
		sb.WriteByte('\n')
	}
	if traceID != "" {
		sb.WriteString("trace: ")
		sb.WriteString(traceID)
		sb.WriteByte('\n')
	}
	sb.WriteString("deleted: ")
	sb.WriteString(when.UTC().Format(time.RFC3339))
	sb.WriteByte('\n')
	sb.WriteString(envelopeEnd)
	sb.WriteString("\n\n")
	sb.WriteString(entrySep)
	sb.WriteByte('\n')
	return sb.String()
}

func formatDeleteTagsEntry(tags []string, agent, traceID string, when time.Time) string {
	var sb strings.Builder
	sb.WriteString("\n## [delete-tags: ")
	sb.WriteString(strings.Join(tags, ", "))
	sb.WriteString("]\n\n")
	sb.WriteString(envelopeOpen)
	sb.WriteByte('\n')
	sb.WriteString("op: delete_tags\n")
	if agent != "" {
		sb.WriteString("agent: ")
		sb.WriteString(agent)
		sb.WriteByte('\n')
	}
	if traceID != "" {
		sb.WriteString("trace: ")
		sb.WriteString(traceID)
		sb.WriteByte('\n')
	}
	sb.WriteString("tags: ")
	sb.WriteString(strings.Join(tags, ", "))
	sb.WriteByte('\n')
	sb.WriteString("deleted: ")
	sb.WriteString(when.UTC().Format(time.RFC3339))
	sb.WriteByte('\n')
	sb.WriteString(envelopeEnd)
	sb.WriteString("\n\n")
	sb.WriteString(entrySep)
	sb.WriteByte('\n')
	return sb.String()
}

// --- Parsing ---

// mdEntry is the raw parsed form of a single section. It is what the
// rescan loop replays into the SQLite index.
type mdEntry struct {
	Op         string
	Entity     string
	Predicate  string
	Agent      string
	TraceID    string
	Tags       []string
	TTL        int
	Created    time.Time
	Updated    time.Time
	Deleted    time.Time
	Body       string
	Prev       string // hash of the entry before it in the chain
	Hash       string // this entry's seal (chain.go)
	SourceFile string
	SourceLine int
}

// parseDailyFile parses one markdown file into a chronological list of
// entries. Returns entries in the order they appear in the file.
func parseDailyFile(path string) ([]mdEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseEntries(f, path)
}

// parseEntries parses markdown entries from r; path is recorded as their
// source file.
func parseEntries(f io.Reader, path string) ([]mdEntry, error) {

	var (
		entries []mdEntry
		scanner = bufio.NewScanner(f)
		lineNo  int
		state   = "idle"
		current mdEntry
		bodyBuf strings.Builder
	)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)

	for scanner.Scan() {
		lineNo++
		line := scanner.Text()

		switch state {
		case "idle":
			if strings.HasPrefix(line, "## ") {
				current = mdEntry{
					Entity:     strings.TrimSpace(strings.TrimPrefix(line, "## ")),
					SourceFile: path,
					SourceLine: lineNo,
				}
				bodyBuf.Reset()
				state = "seeking_envelope"
			}
		case "seeking_envelope":
			if strings.TrimSpace(line) == envelopeOpen {
				state = "in_envelope"
			}
		case "in_envelope":
			if strings.TrimSpace(line) == envelopeEnd {
				state = "in_body"
				continue
			}
			parseEnvelopeLine(&current, line)
		case "in_body":
			if strings.TrimSpace(line) == entrySep {
				current.Body = strings.Trim(bodyBuf.String(), "\n")
				entries = append(entries, current)
				state = "idle"
				continue
			}
			bodyBuf.WriteString(line)
			bodyBuf.WriteByte('\n')
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func parseEnvelopeLine(e *mdEntry, line string) {
	idx := strings.Index(line, ":")
	if idx < 0 {
		return
	}
	key := strings.TrimSpace(line[:idx])
	val := strings.TrimSpace(line[idx+1:])
	switch key {
	case "op":
		e.Op = val
	case "predicate":
		e.Predicate = val
	case "agent":
		e.Agent = val
	case "trace":
		e.TraceID = val
	case "prev":
		e.Prev = val
	case "hash":
		e.Hash = val
	case "tags":
		if val == "" {
			return
		}
		parts := strings.Split(val, ",")
		e.Tags = e.Tags[:0]
		for _, p := range parts {
			if t := strings.TrimSpace(p); t != "" {
				e.Tags = append(e.Tags, t)
			}
		}
	case "ttl":
		fmt.Sscanf(val, "%d", &e.TTL)
	case "created":
		if t, err := time.Parse(time.RFC3339, val); err == nil {
			e.Created = t
		}
	case "updated":
		if t, err := time.Parse(time.RFC3339, val); err == nil {
			e.Updated = t
		}
	case "deleted":
		if t, err := time.Parse(time.RFC3339, val); err == nil {
			e.Deleted = t
		}
	}
}

// listDailyFiles returns the daily log files in the workspace, sorted
// lexicographically (which equals chronologically for YYYY-MM-DD.md).
func listDailyFiles(root string) ([]string, error) {
	dir := filepath.Join(root, workspaceDir, memoryDir)
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var files []string
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		files = append(files, filepath.Join(dir, name))
	}
	sort.Strings(files)
	return files, nil
}
