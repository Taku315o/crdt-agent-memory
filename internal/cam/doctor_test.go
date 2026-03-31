package cam

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"crdt-agent-memory/internal/config"
)

func TestEmbeddingProviderHealthyRuriHTTPRequires2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Fatalf("path = %s, want /healthz", r.URL.Path)
		}
		http.Error(w, "unhealthy", http.StatusBadGateway)
	}))
	defer server.Close()

	ok, detail := embeddingProviderHealthy(context.Background(), config.Config{
		Embedding: config.Embedding{
			Provider:  "ruri-http",
			BaseURL:   server.URL + "/embed",
			TimeoutMS: 1000,
		},
	})
	if ok {
		t.Fatalf("ok = true, want false; detail=%s", detail)
	}
}

func TestEmbeddingProviderHealthyOpenAIRequiresAPIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	ok, detail := embeddingProviderHealthy(context.Background(), config.Config{
		Embedding: config.Embedding{
			Provider: "openai",
			Model:    "text-embedding-3-small",
		},
	})
	if ok {
		t.Fatalf("ok = true, want false; detail=%s", detail)
	}
}

func TestEmbeddingProviderHealthyOpenAIWithAPIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	ok, detail := embeddingProviderHealthy(context.Background(), config.Config{
		Embedding: config.Embedding{
			Provider: "openai",
			Model:    "text-embedding-3-small",
		},
	})
	if !ok {
		t.Fatalf("ok = false, want true; detail=%s", detail)
	}
}

func TestEmbeddingProviderHealthyLocal(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	ok, detail := embeddingProviderHealthy(context.Background(), config.Config{})
	if !ok {
		t.Fatalf("ok = false, want true; detail=%s", detail)
	}
}
