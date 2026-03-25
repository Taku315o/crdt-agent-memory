package memory

import "testing"

func TestNormalizeRecallRequestPreservesExplicitIncludeFlags(t *testing.T) {
	tests := []struct {
		name string
		in   RecallRequest
		want RecallRequest
	}{
		{
			name: "defaults to private and shared when no flags are set",
			in:   RecallRequest{},
			want: RecallRequest{IncludePrivate: true, IncludeShared: true},
		},
		{
			name: "keeps private only",
			in: RecallRequest{
				IncludePrivate: true,
			},
			want: RecallRequest{
				IncludePrivate: true,
			},
		},
		{
			name: "keeps shared only",
			in: RecallRequest{
				IncludeShared: true,
			},
			want: RecallRequest{
				IncludeShared: true,
			},
		},
		{
			name: "keeps transcript only",
			in: RecallRequest{
				IncludeTranscript: true,
			},
			want: RecallRequest{
				IncludeTranscript: true,
			},
		},
		{
			name: "keeps private and transcript without forcing shared",
			in: RecallRequest{
				IncludePrivate:    true,
				IncludeTranscript: true,
			},
			want: RecallRequest{
				IncludePrivate:    true,
				IncludeTranscript: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeRecallRequest(tt.in)
			if got.IncludePrivate != tt.want.IncludePrivate {
				t.Fatalf("IncludePrivate = %v, want %v", got.IncludePrivate, tt.want.IncludePrivate)
			}
			if got.IncludeShared != tt.want.IncludeShared {
				t.Fatalf("IncludeShared = %v, want %v", got.IncludeShared, tt.want.IncludeShared)
			}
			if got.IncludeTranscript != tt.want.IncludeTranscript {
				t.Fatalf("IncludeTranscript = %v, want %v", got.IncludeTranscript, tt.want.IncludeTranscript)
			}
			if got.Query != tt.want.Query {
				t.Fatalf("Query = %q, want %q", got.Query, tt.want.Query)
			}
			if got.ProjectKey != tt.want.ProjectKey {
				t.Fatalf("ProjectKey = %q, want %q", got.ProjectKey, tt.want.ProjectKey)
			}
			if got.BranchName != tt.want.BranchName {
				t.Fatalf("BranchName = %q, want %q", got.BranchName, tt.want.BranchName)
			}
		})
	}
}
