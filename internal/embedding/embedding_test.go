package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"crdt-agent-memory/internal/config"
)

func TestRuriHTTPProviderUsesConfiguredModelAndDimension(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req["input"] != "日本語クエリ" {
			t.Fatalf("input = %q, want 日本語クエリ", req["input"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embedding": []float64{1, 2, 3, 4},
			"model":     "cl-nagoya-ruri-v3",
			"dimension": 4,
		})
	}))
	defer server.Close()

	Configure(config.Embedding{
		Provider:  "ruri-http",
		Model:     "cl-nagoya-ruri-v3",
		BaseURL:   server.URL,
		Dimension: 4,
		TimeoutMS: 1000,
	})
	t.Cleanup(func() { Configure(config.Embedding{}) })

	vec, err := FromText(context.Background(), "日本語クエリ")
	if err != nil {
		t.Fatal(err)
	}
	if len(vec) != 4 {
		t.Fatalf("len(vec) = %d, want 4", len(vec))
	}
}

func TestRuriHTTPProviderRejectsDimensionMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embedding": []float64{1, 2, 3},
			"model":     "cl-nagoya-ruri-v3",
			"dimension": 3,
		})
	}))
	defer server.Close()

	Configure(config.Embedding{
		Provider:  "ruri-http",
		Model:     "cl-nagoya-ruri-v3",
		BaseURL:   server.URL,
		Dimension: 4,
		TimeoutMS: 1000,
	})
	t.Cleanup(func() { Configure(config.Embedding{}) })

	if _, err := FromText(context.Background(), "日本語クエリ"); err == nil {
		t.Fatal("expected dimension mismatch error")
	}
}
