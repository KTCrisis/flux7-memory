package memory

import "testing"

func TestExtractScores(t *testing.T) {
	tests := []struct {
		name     string
		response string
		expected int
		wantNil  bool
		want     []float64
	}{
		{"clean array", "[8, 3, 7, 5]", 4, false, []float64{8, 3, 7, 5}},
		{"with preamble", "Here are the scores: [9, 2, 6]", 3, false, []float64{9, 2, 6}},
		{"with trailing text", "[10, 0, 5]\nDone.", 3, false, []float64{10, 0, 5}},
		{"wrong count", "[8, 3]", 3, true, nil},
		{"no array", "I think doc 1 is best", 3, true, nil},
		{"empty response", "", 3, true, nil},
		{"floats", "[7.5, 3.2, 9.1]", 3, false, []float64{7.5, 3.2, 9.1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractScores(tt.response, tt.expected)
			if tt.wantNil {
				if got != nil {
					t.Errorf("expected nil, got %v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected scores, got nil")
			}
			if len(got) != len(tt.want) {
				t.Fatalf("len mismatch: got %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("score[%d] = %f, want %f", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestTruncateFacts(t *testing.T) {
	facts := []fact{{Entity: "a"}, {Entity: "b"}, {Entity: "c"}}
	if got := truncateFacts(facts, 2); len(got) != 2 {
		t.Errorf("truncate(3, 2) = %d items", len(got))
	}
	if got := truncateFacts(facts, 5); len(got) != 3 {
		t.Errorf("truncate(3, 5) = %d items", len(got))
	}
}
