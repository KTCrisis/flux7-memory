package memory

import (
	"fmt"
	"strings"
	"time"
)

// timeArg reads an optional moment from tool arguments: RFC3339
// ("2026-03-20T09:00:00Z") or a calendar day ("2026-03-20", midnight UTC).
func timeArg(args map[string]any, name string) (time.Time, error) {
	raw, _ := args[name].(string)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("%s: %q is not a date (2026-03-20) or an RFC3339 time", name, raw)
}

// whenArgs reads the moment a read looks at: as_of (what mem7 believed
// then) and valid_at (what held then). Both absent: now.
func whenArgs(args map[string]any) (temporal, error) {
	var t temporal
	var err error
	if t.AsOf, err = timeArg(args, "as_of"); err != nil {
		return t, err
	}
	if t.ValidAt, err = timeArg(args, "valid_at"); err != nil {
		return t, err
	}
	return t, nil
}

// validity renders a version's validity for text results, or "" when it is
// the default (holding since it was written, no end).
func validity(f fact) string {
	if f.ValidTo.IsZero() && (f.ValidFrom.IsZero() || f.ValidFrom.Equal(f.TxFrom)) {
		return ""
	}
	from, to := "…", "now"
	if !f.ValidFrom.IsZero() {
		from = f.ValidFrom.UTC().Format(time.RFC3339)
	}
	if !f.ValidTo.IsZero() {
		to = f.ValidTo.UTC().Format(time.RFC3339)
	}
	return from + " → " + to
}
