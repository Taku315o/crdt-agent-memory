package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net/http"
	"os"
	"strings"
	"sync"
	"unicode"
)

const Dimension = 8

type Provider interface {
	Embed(ctx context.Context, text string) ([]float64, error)
}

var (
	providerOnce sync.Once
	defaultProv  Provider
	defaultErr   error
)

func FromText(ctx context.Context, text string) ([]float64, error) {
	prov, err := defaultProvider()
	if err != nil {
		return nil, err
	}
	return prov.Embed(ctx, text)
}

func LocalFromText(text string) []float64 {
	return localEmbed(text)
}

func defaultProvider() (Provider, error) {
	providerOnce.Do(func() {
		defaultProv, defaultErr = providerFromEnv()
	})
	return defaultProv, defaultErr
}

func providerFromEnv() (Provider, error) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("EMBEDDING_PROVIDER"))) {
	case "", "local":
		return localProvider{}, nil
	case "openai":
		return newOpenAIProviderFromEnv()
	default:
		return nil, fmt.Errorf("unsupported embedding provider %q", os.Getenv("EMBEDDING_PROVIDER"))
	}
}

type localProvider struct{}

func (localProvider) Embed(_ context.Context, text string) ([]float64, error) {
	return localEmbed(text), nil
}

func localEmbed(text string) []float64 {
	tokens := tokenize(text)
	vec := make([]float64, Dimension)
	if len(tokens) == 0 {
		return vec
	}

	for i, token := range tokens {
		weight := 1.0
		if len(token) >= 6 {
			weight += 0.25
		}
		if hasDigit(token) {
			weight += 0.15
		}

		vec[hashBucket(token)] += weight
		if i+1 < len(tokens) {
			bigram := token + " " + tokens[i+1]
			vec[hashBucket(bigram)] += 0.5
		}
	}

	return normalize(vec)
}

func tokenize(text string) []string {
	text = strings.ToLower(text)
	var tokens []string
	var current strings.Builder
	flush := func() {
		if current.Len() == 0 {
			return
		}
		token := current.String()
		current.Reset()
		if isStopWord(token) {
			return
		}
		tokens = append(tokens, token)
	}

	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return tokens
}

func hasDigit(token string) bool {
	for _, r := range token {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func isStopWord(token string) bool {
	switch token {
	case "", "a", "an", "and", "are", "as", "at", "be", "but", "by", "for", "from", "has", "have", "i", "in", "is", "it", "of", "on", "or", "that", "the", "to", "was", "were", "with", "you":
		return true
	default:
		return false
	}
}

func hashBucket(token string) int {
	h := fnv.New64a()
	_, _ = h.Write([]byte(token))
	return int(h.Sum64() % Dimension)
}

func normalize(vec []float64) []float64 {
	var sum float64
	for _, v := range vec {
		sum += v * v
	}
	if sum == 0 {
		return vec
	}
	norm := 1 / sqrt(sum)
	for i := range vec {
		vec[i] *= norm
	}
	return vec
}

func sqrt(v float64) float64 {
	if v <= 0 {
		return 0
	}
	x := v
	for i := 0; i < 12; i++ {
		x = 0.5 * (x + v/x)
	}
	return x
}

type openAIProvider struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
}

func newOpenAIProviderFromEnv() (Provider, error) {
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		return nil, errors.New("OPENAI_API_KEY is required when EMBEDDING_PROVIDER=openai")
	}
	model := strings.TrimSpace(os.Getenv("OPENAI_EMBEDDING_MODEL"))
	if model == "" {
		model = "text-embedding-3-small"
	}
	baseURL := strings.TrimSpace(os.Getenv("OPENAI_API_BASE"))
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1/embeddings"
	}
	return openAIProvider{
		apiKey:     apiKey,
		model:      model,
		baseURL:    baseURL,
		httpClient: http.DefaultClient,
	}, nil
}

type openAIEmbeddingResponse struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error,omitempty"`
}

func (p openAIProvider) Embed(ctx context.Context, text string) ([]float64, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("embedding text is required")
	}
	payload := map[string]any{
		"model": p.model,
		"input": text,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var parsed openAIEmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if parsed.Error != nil && parsed.Error.Message != "" {
			return nil, fmt.Errorf("openai embeddings api: %s", parsed.Error.Message)
		}
		return nil, fmt.Errorf("openai embeddings api: unexpected status %s", resp.Status)
	}
	if len(parsed.Data) == 0 || len(parsed.Data[0].Embedding) == 0 {
		return nil, errors.New("openai embeddings api returned no embedding data")
	}
	return compressEmbedding(parsed.Data[0].Embedding, Dimension), nil
}

func compressEmbedding(values []float64, dimension int) []float64 {
	if dimension <= 0 {
		return nil
	}
	out := make([]float64, dimension)
	if len(values) == 0 {
		return out
	}
	counts := make([]int, dimension)
	for i, value := range values {
		slot := i % dimension
		out[slot] += value
		counts[slot]++
	}
	for i := range out {
		if counts[i] > 0 {
			out[i] /= float64(counts[i])
		}
	}
	return normalize(out)
}
