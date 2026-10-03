package memory

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The markdown workspace is a hash chain. Every entry mem7 writes (a store,
// a deletion, a deletion by tags) carries the seal of the entry before it
// (`prev:`) and its own (`hash:`), computed over its parsed fields. Change an
// entry, drop one, or reorder them, and `mem7 verify` points at the first
// place the chain no longer holds.
//
// With a key (MEM7_CHAIN_KEY), the seal is an HMAC-SHA256: whoever edits the
// workspace without the key cannot reseal what follows. Without one it is a
// plain SHA-256, which catches accidents and careless edits, not a forger.
// The key must not change once entries are sealed with it.
//
// Entries written before the chain existed carry no seal; the chain starts
// at the first sealed entry, and verify counts the older ones as legacy.

// chainVersion prefixes the sealed text, so the format can evolve.
const chainVersion = "mem7-chain-v1"

// canonical is the text an entry's seal covers: its fields as mem7 parses
// them, in a fixed order, after the previous seal. Whitespace around the
// envelope does not count; any change to a value does.
func canonical(e mdEntry, prev string) string {
	var sb strings.Builder
	field := func(k, v string) {
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(strconv.Quote(v))
		sb.WriteByte('\n')
	}
	ts := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format(time.RFC3339)
	}
	field("v", chainVersion)
	field("prev", prev)
	field("op", e.Op)
	field("entity", e.Entity)
	field("predicate", e.Predicate)
	field("agent", e.Agent)
	field("trace", e.TraceID)
	field("tags", strings.Join(e.Tags, ","))
	field("ttl", strconv.Itoa(e.TTL))
	field("created", ts(e.Created))
	field("updated", ts(e.Updated))
	field("deleted", ts(e.Deleted))
	field("body", e.Body)
	return sb.String()
}

// seal is an entry's hash: HMAC-SHA256 with the key, SHA-256 without.
func seal(key []byte, e mdEntry, prev string) string {
	text := []byte(canonical(e, prev))
	if len(key) > 0 {
		m := hmac.New(sha256.New, key)
		m.Write(text)
		return hex.EncodeToString(m.Sum(nil))
	}
	sum := sha256.Sum256(text)
	return hex.EncodeToString(sum[:])
}

// appendSealed seals a formatted entry against the chain head, writes it,
// and moves the head only once the write succeeded.
func (w *markdownWriter) appendSealed(when time.Time, block string) (string, int, error) {
	entries, err := parseEntries(strings.NewReader(block), "")
	if err != nil || len(entries) != 1 {
		return "", 0, fmt.Errorf("seal: entry does not parse back (%v)", err)
	}
	h := seal(w.key, entries[0], w.head)

	// the seal goes last in the envelope, before its closing fence
	open := strings.Index(block, envelopeOpen+"\n")
	if open < 0 {
		return "", 0, fmt.Errorf("seal: no envelope")
	}
	closing := strings.Index(block[open+len(envelopeOpen)+1:], envelopeEnd+"\n")
	if closing < 0 {
		return "", 0, fmt.Errorf("seal: unterminated envelope")
	}
	at := open + len(envelopeOpen) + 1 + closing
	var lines strings.Builder
	if w.head != "" {
		lines.WriteString("prev: " + w.head + "\n")
	}
	lines.WriteString("hash: " + h + "\n")
	sealed := block[:at] + lines.String() + block[at:]

	path, line, err := w.appendToDaily(when, sealed)
	if err != nil {
		return "", 0, err
	}
	w.head = h
	return path, line, nil
}

// loadHead sets the chain head to the last sealed entry of the workspace.
func (w *markdownWriter) loadHead() error {
	files, err := listDailyFiles(w.root)
	if err != nil {
		return err
	}
	for i := len(files) - 1; i >= 0; i-- {
		entries, err := parseDailyFile(files[i])
		if err != nil {
			return err
		}
		for j := len(entries) - 1; j >= 0; j-- {
			if entries[j].Hash != "" {
				w.head = entries[j].Hash
				return nil
			}
		}
	}
	return nil
}

// ChainReport is what VerifyChain found.
type ChainReport struct {
	Entries int  `json:"entries"` // entries read
	Legacy  int  `json:"legacy"`  // entries before the chain started (no seal)
	Sealed  int  `json:"sealed"`  // entries whose seal holds
	Keyed   bool `json:"keyed"`   // seals were checked with a key (HMAC)
	// Break is the first place the chain does not hold; nil when it holds.
	Break *ChainBreak `json:"break"`
}

// ChainBreak locates the first entry the chain does not vouch for.
type ChainBreak struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Entity string `json:"entity"`
	Reason string `json:"reason"`
}

// VerifyChain walks every entry of the workspace in order and checks each
// seal against its fields and the seal before it.
func VerifyChain(root string, key []byte) (ChainReport, error) {
	r := ChainReport{Keyed: len(key) > 0}
	files, err := listDailyFiles(root)
	if err != nil {
		return r, err
	}
	head, started := "", false
	for _, file := range files {
		entries, err := parseDailyFile(file)
		if err != nil {
			return r, err
		}
		for _, e := range entries {
			r.Entries++
			if e.Hash == "" {
				if !started {
					r.Legacy++
					continue
				}
				r.Break = &ChainBreak{file, e.SourceLine, e.Entity, "entry without a seal after the chain started"}
				return r, nil
			}
			if started && e.Prev != head {
				r.Break = &ChainBreak{file, e.SourceLine, e.Entity, "does not follow the previous seal: an entry was removed, added or reordered before it"}
				return r, nil
			}
			if !started && e.Prev != "" {
				r.Break = &ChainBreak{file, e.SourceLine, e.Entity, "the first sealed entry points to a predecessor that is missing"}
				return r, nil
			}
			if seal(key, e, e.Prev) != e.Hash {
				reason := "content changed since it was sealed"
				if len(key) == 0 {
					reason += " (or it was sealed with a key: set MEM7_CHAIN_KEY)"
				}
				r.Break = &ChainBreak{file, e.SourceLine, e.Entity, reason}
				return r, nil
			}
			started, head = true, e.Hash
			r.Sealed++
		}
	}
	return r, nil
}

// SetChainKey makes new seals HMACs with key. Call before the first write.
func (s *Store) SetChainKey(key []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.md.key = key
}

// VerifyChain checks the workspace's chain with the store's key.
func (s *Store) VerifyChain() (ChainReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return VerifyChain(s.dir, s.md.key)
}
