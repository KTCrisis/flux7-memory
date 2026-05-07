package memory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

type reranker struct {
	url    string
	model  string
	client *http.Client
}

func newReranker(url, model string) *reranker {
	if model == "" {
		model = "gemma4:e4b"
	}
	return &reranker{
		url:    url,
		model:  model,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

func (r *reranker) Rerank(query string, candidates []fact, limit int) ([]fact, error) {
	if len(candidates) <= 1 || limit <= 0 {
		return candidates, nil
	}

	var sb strings.Builder
	sb.WriteString("Query: ")
	sb.WriteString(query)
	sb.WriteString("\n\nDocuments:\n")
	for i, f := range candidates {
		text := strings.ReplaceAll(f.Object, "\n", " ")
		if len(text) > 300 {
			text = text[:300]
		}
		sb.WriteString(fmt.Sprintf("[%d] %s: %s\n", i, f.Entity, text))
	}
	sb.WriteString(fmt.Sprintf("\nRate each document's relevance to the query from 0 to 10. Return ONLY a JSON array of %d integer scores.", len(candidates)))

	body, _ := json.Marshal(map[string]any{
		"model":  r.model,
		"prompt": sb.String(),
		"stream": false,
		"think":  false,
		"options": map[string]any{
			"temperature": 0.0,
			"num_predict": len(candidates)*4 + 20,
		},
	})

	resp, err := r.client.Post(r.url+"/api/generate", "application/json", bytes.NewReader(body))
	if err != nil {
		return truncateFacts(candidates, limit), nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return truncateFacts(candidates, limit), nil
	}

	var result struct {
		Response string `json:"response"`
		Thinking string `json:"thinking"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return truncateFacts(candidates, limit), nil
	}

	text := result.Response
	if text == "" {
		text = result.Thinking
	}
	scores := extractScores(text, len(candidates))
	if scores == nil {
		return truncateFacts(candidates, limit), nil
	}

	type ranked struct {
		idx   int
		score float64
	}
	items := make([]ranked, len(candidates))
	for i := range candidates {
		items[i] = ranked{idx: i, score: scores[i]}
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].score > items[j].score
	})

	n := limit
	if n > len(items) {
		n = len(items)
	}
	out := make([]fact, n)
	for i := 0; i < n; i++ {
		out[i] = candidates[items[i].idx]
	}
	return out, nil
}

func extractScores(response string, expected int) []float64 {
	start := strings.Index(response, "[")
	end := strings.LastIndex(response, "]")
	if start < 0 || end <= start {
		return nil
	}
	var scores []float64
	if err := json.Unmarshal([]byte(response[start:end+1]), &scores); err != nil {
		return nil
	}
	if len(scores) != expected {
		return nil
	}
	return scores
}

func truncateFacts(facts []fact, limit int) []fact {
	if limit >= len(facts) {
		return facts
	}
	return facts[:limit]
}
