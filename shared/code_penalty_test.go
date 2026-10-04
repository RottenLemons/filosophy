package shared

import "testing"

func TestCodeFileMultiplier(t *testing.T) {
	const codePath = `C:\Projects\sample\internal\search.go`

	tests := []struct {
		name    string
		query   string
		matches map[string]bool
		path    string
		want    float64
	}{
		{name: "generic query downranks source", query: "find recent notes", path: codePath, want: codeFileScoreMultiplier},
		{name: "exact identifier filename", query: "search", path: codePath, want: 1},
		{name: "near exact filename typo", query: "searck", path: codePath, want: 1},
		{name: "exact content phrase", query: "find recent notes", matches: map[string]bool{codePath: true}, path: codePath, want: 1},
		{name: "documents unaffected", query: "find recent notes", path: `C:\Documents\notes.pdf`, want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := codeFileMultiplier(tt.path, tt.query, tt.matches); got != tt.want {
				t.Errorf("codeFileMultiplier() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestApplyCodeFilePenaltyRanksDocumentsFirstButKeepsExactCodeMatch(t *testing.T) {
	results := []SearchResult{
		{Path: `C:\Projects\sample\search.go`, Score: 0.8},
		{Path: `C:\Documents\notes.pdf`, Score: 0.2},
	}

	applyCodeFilePenalty(results, "recent notes", nil)
	if results[0].Path != `C:\Documents\notes.pdf` {
		t.Fatalf("generic query top result = %q, want document", results[0].Path)
	}
	if got := results[1].Score; got != 0.8*codeFileScoreMultiplier {
		t.Fatalf("penalized code score = %v, want %v", got, 0.8*codeFileScoreMultiplier)
	}

	results = []SearchResult{
		{Path: `C:\Projects\sample\search.go`, Score: 0.8},
		{Path: `C:\Documents\notes.pdf`, Score: 0.2},
	}
	applyCodeFilePenalty(results, "search", nil)
	if results[0].Path != `C:\Projects\sample\search.go` {
		t.Fatalf("exact filename query top result = %q, want source file", results[0].Path)
	}
}
