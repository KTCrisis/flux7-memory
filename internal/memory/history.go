package memory

import (
	"fmt"
	"strings"
	"time"
)

// ToolHistory is ToolHistoryAs for an unscoped caller.
func (s *Store) ToolHistory(args map[string]any) Result { return s.ToolHistoryAs(args, Caller{}) }

// ToolHistoryAs returns the life of one key, oldest first, read from the
// markdown workspace: every store, update and deletion (by key or by tags),
// with its author, the trace of the governed call behind it, and its seal.
// The index only knows the current state; the workspace keeps all of it.
func (s *Store) ToolHistoryAs(args map[string]any, c Caller) Result {
	s.mu.Lock()
	defer s.mu.Unlock()

	key, _ := args["key"].(string)
	if key == "" {
		return ErrResult("key is required")
	}
	files, err := listDailyFiles(s.dir)
	if err != nil {
		return ErrResult(fmt.Sprintf("list workspace: %v", err))
	}

	type event struct {
		when                               time.Time
		what, agent, trace, hash, validity string
	}
	var (
		events []event
		tags   []string // the key's tags as of the current entry
		owner  string
		live   bool
	)
	for _, file := range files {
		entries, err := parseDailyFile(file)
		if err != nil {
			return ErrResult(fmt.Sprintf("read %s: %v", file, err))
		}
		for _, e := range entries {
			switch {
			case e.Op == "store" && e.Entity == key:
				what := "store"
				if live {
					what = "update"
				}
				events = append(events, event{e.Updated, what, e.Agent, e.TraceID, e.Hash, entryValidity(e)})
				tags, owner, live = e.Tags, e.Agent, true
			case e.Op == "delete" && e.Entity == key && live:
				events = append(events, event{e.Deleted, "delete", e.Agent, e.TraceID, e.Hash, ""})
				live = false
			case e.Op == "delete_tags" && live && hasAll(tags, e.Tags):
				events = append(events, event{e.Deleted, "delete by tags " + strings.Join(e.Tags, ", "), e.Agent, e.TraceID, e.Hash, ""})
				live = false
			}
		}
	}
	if len(events) == 0 || (s.scoped(c) && !s.scopes.canRead(c.Agent, owner)) {
		return TextResult("No history.")
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "History of %s (%d events, oldest first):\n", key, len(events))
	for _, ev := range events {
		fmt.Fprintf(&sb, "- %s %s", ev.when.UTC().Format(time.RFC3339), ev.what)
		if ev.agent != "" {
			fmt.Fprintf(&sb, " by %s", ev.agent)
		}
		if ev.validity != "" {
			fmt.Fprintf(&sb, " · valid %s", ev.validity)
		}
		if ev.trace != "" {
			fmt.Fprintf(&sb, " · trace %s", ev.trace)
		}
		if ev.hash != "" {
			fmt.Fprintf(&sb, " · seal %s", ev.hash[:12])
		} else {
			sb.WriteString(" · unsealed (written before the chain)")
		}
		sb.WriteByte('\n')
	}
	return TextResult(sb.String())
}

// hasAll reports whether have contains every tag of want.
func hasAll(have, want []string) bool {
	set := make(map[string]bool, len(have))
	for _, t := range have {
		set[t] = true
	}
	for _, t := range want {
		if !set[t] {
			return false
		}
	}
	return len(want) > 0
}

// entryValidity is the validity a store entry declared, or "" when it holds
// from the moment it was written with no end (the default).
func entryValidity(e mdEntry) string {
	if e.ValidFrom.IsZero() && e.ValidTo.IsZero() {
		return ""
	}
	vf := e.ValidFrom
	if vf.IsZero() {
		vf = e.Updated
	}
	return validity(fact{ValidFrom: vf, ValidTo: e.ValidTo, TxFrom: e.Updated})
}
