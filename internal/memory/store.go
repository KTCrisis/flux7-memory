package memory

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Store is the v0.2.0 orchestrator. It owns the markdown workspace
// (source of truth) and the derived SQLite index, and routes every
// MCP tool call through both in the correct order : write to markdown
// first, then update the index. On read it consults only the index.
type Store struct {
	dir      string
	md       *markdownWriter
	index    storage
	maxCount int
	mu       sync.Mutex
	emb      *embedder
	embCache map[int64][]float32
	rnk      *reranker
}

func (s *Store) SetEmbedder(url, model, provider, key string) {
	s.emb = newEmbedder(url, model, provider, key)
}

func (s *Store) SetReranker(url, model string) {
	s.rnk = newReranker(url, model)
}

// NewStore constructs a Store rooted at dir, opens (or creates) the
// SQLite index, and prepares the markdown workspace.
func NewStore(dir string, maxEntries int) (*Store, error) {
	if maxEntries <= 0 {
		maxEntries = 10000
	}
	// First run on a fresh machine: the data dir does not exist yet and
	// SQLite refuses to create its file in a missing directory (SQLITE_CANTOPEN).
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	idx, err := newSQLiteStore(dir)
	if err != nil {
		return nil, err
	}
	return &Store{
		dir:      dir,
		md:       newMarkdownWriter(dir),
		index:    idx,
		maxCount: maxEntries,
	}, nil
}

// Close releases the underlying index handle.
func (s *Store) Close() error {
	return s.index.Close()
}

// SnapshotReminder returns a structured instructional payload that an
// agent runtime can inject into its prompt when context pressure is
// detected. It encourages the agent to persist important state into
// mem7 before the next compaction, and surfaces the current workspace
// path and live entry count as grounding.
//
// Consumed by the "memory/snapshot_reminder" MCP method and by the
// HTTP /memory/snapshot_reminder convenience endpoint.
func (s *Store) SnapshotReminder() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()

	count, _ := s.index.Count()
	workspace := filepath.Join(s.dir, workspaceDir)
	return map[string]any{
		"reminder": "Context compaction is imminent. Before losing detail, persist any important state into mem7 with memory_store(key, value[, tags, agent]). Use descriptive keys. Long-term facts go to the workspace ; ephemeral session state can carry a TTL.",
		"workspace":    workspace,
		"memory_count": count,
		"generated_at": time.Now().UTC().Format(time.RFC3339),
	}
}

// --- Tool methods ---

func (s *Store) ToolStore(args map[string]any) Result {
	s.mu.Lock()
	defer s.mu.Unlock()

	key, _ := args["key"].(string)
	value, _ := args["value"].(string)
	if key == "" || value == "" {
		return ErrResult("key and value are required")
	}

	tags := parseTags(args["tags"])
	agent, _ := args["agent"].(string)
	ttl := 0
	if v, ok := args["ttl"].(float64); ok {
		ttl = int(v)
	}

	count, err := s.index.Count()
	if err != nil {
		return ErrResult(fmt.Sprintf("index count failed: %v", err))
	}

	existing, err := s.index.Query(filter{Entity: key, Limit: 1})
	if err != nil {
		return ErrResult(fmt.Sprintf("index query failed: %v", err))
	}
	isNew := len(existing) == 0
	if isNew && count >= s.maxCount {
		return ErrResult(fmt.Sprintf("memory full: %d entries (max %d)", count, s.maxCount))
	}

	now := time.Now().UTC()
	f := fact{
		Entity:    key,
		Predicate: defaultPredicate,
		Object:    value,
		Tags:      tags,
		Agent:     agent,
		TTL:       ttl,
		Updated:   now,
		Created:   now,
	}
	if !isNew {
		f.Created = existing[0].Created
		if agent == "" {
			f.Agent = existing[0].Agent
		}
	}

	path, line, err := s.md.AppendStore(f)
	if err != nil {
		return ErrResult(fmt.Sprintf("failed to write markdown: %v", err))
	}
	f.SourceFile = path
	f.SourceLine = line

	saved, err := s.index.Put(f)
	if err != nil {
		return ErrResult(fmt.Sprintf("failed to update index: %v", err))
	}

	if s.emb != nil {
		if vec, err := s.emb.Embed(f.Object); err == nil {
			_ = s.index.StoreEmbedding(saved.ID, vec)
			if s.embCache != nil {
				s.embCache[saved.ID] = vec
			}
		}
	}

	newCount, _ := s.index.Count()
	action := "created"
	if !isNew {
		action = "updated"
	}
	return TextResult(fmt.Sprintf("Memory '%s' %s (%d total entries)", key, action, newCount))
}

func (s *Store) ToolRecall(args map[string]any) Result {
	s.mu.Lock()
	defer s.mu.Unlock()

	key, _ := args["key"].(string)
	tags := parseTags(args["tags"])
	agent, _ := args["agent"].(string)
	limit := 10
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}

	results, err := s.index.Query(filter{
		Entity: key,
		Tags:   tags,
		Agent:  agent,
		Limit:  limit,
	})
	if err != nil {
		return ErrResult(fmt.Sprintf("query failed: %v", err))
	}
	if len(results) == 0 {
		return TextResult("No memories found.")
	}
	s.touchAccessed(results)

	var sb strings.Builder
	for _, f := range results {
		sb.WriteString(fmt.Sprintf("## %s\n", f.Entity))
		sb.WriteString(f.Object)
		if !strings.HasSuffix(f.Object, "\n") {
			sb.WriteString("\n")
		}
		if len(f.Tags) > 0 {
			sb.WriteString(fmt.Sprintf("Tags: %s\n", strings.Join(f.Tags, ", ")))
		}
		if f.Agent != "" {
			sb.WriteString(fmt.Sprintf("Agent: %s\n", f.Agent))
		}
		sb.WriteString(fmt.Sprintf("Updated: %s\n\n", f.Updated.UTC().Format(time.RFC3339)))
	}
	return TextResult(sb.String())
}

func (s *Store) ToolList(args map[string]any) Result {
	s.mu.Lock()
	defer s.mu.Unlock()

	tags := parseTags(args["tags"])
	agent, _ := args["agent"].(string)

	results, err := s.index.List(filter{Tags: tags, Agent: agent})
	if err != nil {
		return ErrResult(fmt.Sprintf("list failed: %v", err))
	}
	if len(results) == 0 {
		return TextResult("No memories found.")
	}

	var sb strings.Builder
	for _, f := range results {
		tagStr := ""
		if len(f.Tags) > 0 {
			tagStr = fmt.Sprintf(" [%s]", strings.Join(f.Tags, ", "))
		}
		agentStr := ""
		if f.Agent != "" {
			agentStr = fmt.Sprintf(" (by %s)", f.Agent)
		}
		sb.WriteString(fmt.Sprintf("- %s%s%s — %s\n",
			f.Entity, tagStr, agentStr, f.Updated.UTC().Format(time.RFC3339)))
	}
	return TextResult(fmt.Sprintf("%d memories:\n%s", len(results), sb.String()))
}

// ToolSearch runs a full-text BM25 search on the memory index.
// Accepts the same tag/agent post-filters as Recall, plus an optional
// time range (since/until as RFC3339 strings).
func (s *Store) ToolSearch(args map[string]any) Result {
	s.mu.Lock()
	defer s.mu.Unlock()

	query, _ := args["query"].(string)
	if strings.TrimSpace(query) == "" {
		return ErrResult("query is required")
	}

	tags := parseTags(args["tags"])
	agent, _ := args["agent"].(string)
	limit := 10
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}

	mode, _ := args["mode"].(string)
	includeNeighbors, _ := args["include_neighbors"].(bool)
	neighborRadius := 1
	if v, ok := args["neighbor_radius"].(float64); ok && v > 0 {
		neighborRadius = int(v)
	}
	q := searchQuery{
		Query:            query,
		Mode:             mode,
		Tags:             tags,
		Agent:            agent,
		Limit:            limit,
		IncludeNeighbors: includeNeighbors,
		NeighborRadius:   neighborRadius,
	}
	if v, ok := args["since"].(string); ok && v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q.Since = t
		}
	}
	if v, ok := args["until"].(string); ok && v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q.Until = t
		}
	}

	var results []fact
	if s.emb != nil {
		var err error
		results, err = s.hybridSearch(q)
		if err != nil {
			return ErrResult(fmt.Sprintf("hybrid search failed: %v", err))
		}
	} else {
		rq := q
		if s.rnk != nil {
			rq.Limit = q.Limit * 3
		}
		var err error
		results, err = s.index.Search(rq)
		if err != nil {
			return ErrResult(fmt.Sprintf("search failed: %v", err))
		}
		if s.rnk != nil && len(results) > 1 {
			results, _ = s.rnk.Rerank(q.Query, results, q.Limit)
		}
	}
	if len(results) == 0 {
		return TextResult("No memories found.")
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d results (ranked by relevance):\n\n", len(results)))
	for _, f := range results {
		sb.WriteString(fmt.Sprintf("## %s\n", f.Entity))
		sb.WriteString(f.Object)
		if !strings.HasSuffix(f.Object, "\n") {
			sb.WriteString("\n")
		}
		if len(f.Tags) > 0 {
			sb.WriteString(fmt.Sprintf("Tags: %s\n", strings.Join(f.Tags, ", ")))
		}
		if f.Agent != "" {
			sb.WriteString(fmt.Sprintf("Agent: %s\n", f.Agent))
		}
		sb.WriteString(fmt.Sprintf("Updated: %s\n\n", f.Updated.UTC().Format(time.RFC3339)))
	}
	return TextResult(sb.String())
}

// ToolGet reads a file from the markdown workspace, optionally
// between from_line and to_line (1-indexed, inclusive). The path is
// resolved relative to <data-dir>/workspace and must not escape it ;
// absolute paths and ".." traversals are refused.
func (s *Store) ToolGet(args map[string]any) Result {
	s.mu.Lock()
	defer s.mu.Unlock()

	rel, _ := args["path"].(string)
	if rel == "" {
		return ErrResult("path is required")
	}

	workspace := filepath.Join(s.dir, workspaceDir)
	full := filepath.Join(workspace, rel)
	// Refuse any path that escapes the workspace after cleaning.
	clean := filepath.Clean(full)
	rootClean := filepath.Clean(workspace)
	if clean != rootClean && !strings.HasPrefix(clean, rootClean+string(filepath.Separator)) {
		return ErrResult("path escapes workspace")
	}

	f, err := os.Open(clean)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrResult(fmt.Sprintf("file not found: %s", rel))
		}
		return ErrResult(fmt.Sprintf("open failed: %v", err))
	}
	defer f.Close()

	fromLine := 0
	if v, ok := args["from_line"].(float64); ok && v > 0 {
		fromLine = int(v)
	}
	toLine := 0
	if v, ok := args["to_line"].(float64); ok && v > 0 {
		toLine = int(v)
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	var sb strings.Builder
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		if fromLine > 0 && lineNo < fromLine {
			continue
		}
		if toLine > 0 && lineNo > toLine {
			break
		}
		sb.WriteString(scanner.Text())
		sb.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return ErrResult(fmt.Sprintf("read failed: %v", err))
	}
	if sb.Len() == 0 {
		return TextResult("(empty range)")
	}
	return TextResult(sb.String())
}

func (s *Store) ToolForget(args map[string]any) Result {
	s.mu.Lock()
	defer s.mu.Unlock()

	key, _ := args["key"].(string)
	tags := parseTags(args["tags"])
	agent, _ := args["agent"].(string)
	now := time.Now().UTC()

	if key == "" && len(tags) == 0 {
		return ErrResult("key or tags required")
	}

	removed := 0
	if key != "" {
		if err := s.md.AppendDelete(key, agent, now); err != nil {
			return ErrResult(fmt.Sprintf("failed to write tombstone: %v", err))
		}
		n, err := s.index.DeleteByEntity(key)
		if err != nil {
			return ErrResult(fmt.Sprintf("delete by entity failed: %v", err))
		}
		removed += n
	}
	if len(tags) > 0 {
		if err := s.md.AppendDeleteTags(tags, agent, now); err != nil {
			return ErrResult(fmt.Sprintf("failed to write tombstone: %v", err))
		}
		n, err := s.index.DeleteByTags(tags)
		if err != nil {
			return ErrResult(fmt.Sprintf("delete by tags failed: %v", err))
		}
		removed += n
	}

	if removed == 0 {
		return TextResult("No matching memories found.")
	}
	remaining, _ := s.index.Count()
	return TextResult(fmt.Sprintf("Removed %d memory(ies). %d remaining.", removed, remaining))
}

// Prune physically removes TTL-expired entries from the index. The
// markdown workspace is left untouched : it stays the immutable
// historical log, and Rescan re-evaluates TTL liveness on replay so
// expired rows never come back.
func (s *Store) Prune() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.index.PurgeExpired()
}

// ToolContext performs the same search as ToolSearch but returns structured
// JSON instead of formatted markdown — designed for SDK/programmatic use.
func (s *Store) ToolContext(args map[string]any) Result {
	s.mu.Lock()
	defer s.mu.Unlock()

	query, _ := args["query"].(string)
	if strings.TrimSpace(query) == "" {
		return ErrResult("query is required")
	}

	tags := parseTags(args["tags"])
	agent, _ := args["agent"].(string)
	limit := 10
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	mode, _ := args["mode"].(string)
	includeNeighbors, _ := args["include_neighbors"].(bool)
	neighborRadius := 1
	if v, ok := args["neighbor_radius"].(float64); ok && v > 0 {
		neighborRadius = int(v)
	}
	q := searchQuery{
		Query:            query,
		Mode:             mode,
		Tags:             tags,
		Agent:            agent,
		Limit:            limit,
		IncludeNeighbors: includeNeighbors,
		NeighborRadius:   neighborRadius,
	}
	if v, ok := args["since"].(string); ok && v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q.Since = t
		}
	}
	if v, ok := args["until"].(string); ok && v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q.Until = t
		}
	}

	var results []fact
	if s.emb != nil {
		var err error
		results, err = s.hybridSearch(q)
		if err != nil {
			return ErrResult(fmt.Sprintf("hybrid search failed: %v", err))
		}
	} else {
		rq := q
		if s.rnk != nil {
			rq.Limit = q.Limit * 3
		}
		var err error
		results, err = s.index.Search(rq)
		if err != nil {
			return ErrResult(fmt.Sprintf("search failed: %v", err))
		}
		if s.rnk != nil && len(results) > 1 {
			results, _ = s.rnk.Rerank(q.Query, results, q.Limit)
		}
	}

	items := make([]map[string]any, len(results))
	for i, f := range results {
		items[i] = map[string]any{
			"key":     f.Entity,
			"value":   f.Object,
			"tags":    f.Tags,
			"agent":   f.Agent,
			"updated": f.Updated.UTC().Format(time.RFC3339),
		}
	}
	data, _ := json.Marshal(items)
	return TextResult(string(data))
}

// Result is the MCP tool-call content envelope returned by every tool
// method. Tool-level errors are carried inside this envelope with
// isError=true ; transport-level errors travel through the JSON-RPC
// error object instead.
type Result = map[string]any

// TextResult wraps plain text in the MCP content envelope.
func TextResult(text string) Result {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
	}
}

// ErrResult wraps an error message in the MCP content envelope with
// isError set. It represents a tool-level failure, not a transport error.
func ErrResult(msg string) Result {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": msg}},
		"isError": true,
	}
}

func (s *Store) hybridSearch(q searchQuery) ([]fact, error) {
	queryVec, err := s.emb.Embed(q.Query)
	if err != nil {
		return s.index.Search(q)
	}

	bm25q := q
	bm25q.Limit = q.Limit * 2
	bm25Results, err := s.index.Search(bm25q)
	if err != nil {
		return nil, err
	}

	if s.embCache == nil {
		s.embCache, _ = s.index.LoadEmbeddings()
		if s.embCache == nil {
			s.embCache = make(map[int64][]float32)
		}
	}

	cosineResults := cosineSearch(queryVec, s.embCache, q.Limit*2)

	factMap := make(map[int64]fact, len(bm25Results))
	for _, f := range bm25Results {
		factMap[f.ID] = f
	}
	var missingIDs []int64
	for _, sc := range cosineResults {
		if _, ok := factMap[sc.ID]; !ok {
			missingIDs = append(missingIDs, sc.ID)
		}
	}
	if len(missingIDs) > 0 {
		missing, err := s.index.FetchByIDs(missingIDs)
		if err != nil {
			return nil, err
		}
		for _, f := range missing {
			factMap[f.ID] = f
		}
	}

	mergeLimit := q.Limit
	if s.rnk != nil {
		mergeLimit = q.Limit * 3
	}
	merged := mergeRRF(bm25Results, cosineResults, factMap, mergeLimit)

	if s.rnk != nil && len(merged) > 1 {
		merged, _ = s.rnk.Rerank(q.Query, merged, q.Limit)
	}

	if q.IncludeNeighbors && len(merged) > 0 {
		radius := q.NeighborRadius
		if radius <= 0 {
			radius = 1
		}
		expanded, err := s.index.(*sqliteStore).expandWithNeighbors(merged, radius)
		if err == nil {
			merged = expanded
		}
	}

	return merged, nil
}

func (s *Store) touchAccessed(results []fact) {
	if len(results) == 0 {
		return
	}
	ids := make([]int64, len(results))
	for i, f := range results {
		ids[i] = f.ID
	}
	_ = s.index.TouchAccessed(ids)
}

func parseTags(v any) []string {
	if v == nil {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	tags := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			tags = append(tags, s)
		}
	}
	return tags
}
