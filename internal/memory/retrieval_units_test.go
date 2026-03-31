package memory

import "testing"

func TestJARankingPrefersExactJapaneseMatch(t *testing.T) {
	rows := []recallCandidateRow{
		{
			key:              recallKey{MemorySpace: "shared", MemoryID: "semantic-first"},
			RecallResult:     RecallResult{UnitID: "semantic-first", MemorySpace: "shared", MemoryID: "semantic-first", Subject: "設計検討", Body: "検索品質を改善する方針", AuthoredAtMS: 100},
			TrustWeight:      1.0,
			SemanticRank:     1,
			LexicalRank:      3,
			SemanticDistance: 0.1,
			LexicalBM25:      0.1,
		},
		{
			key:              recallKey{MemorySpace: "shared", MemoryID: "exact-match"},
			RecallResult:     RecallResult{UnitID: "exact-match", MemorySpace: "shared", MemoryID: "exact-match", Subject: "壁打ち品質", Body: "壁打ち品質を上げる具体策", AuthoredAtMS: 90},
			TrustWeight:      1.0,
			SemanticRank:     2,
			LexicalRank:      1,
			SemanticDistance: 0.2,
			LexicalBM25:      1.0,
		},
	}

	got := rankRetrievalRows(rows, map[recallKey]recallGraphStat{}, map[recallKey]recallArtifactStat{}, 2, "ja-default", "壁打ち品質")
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].MemoryID != "exact-match" {
		t.Fatalf("first memory_id = %q, want exact-match", got[0].MemoryID)
	}
}
