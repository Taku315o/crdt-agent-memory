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
	"time"
	"unicode"

	"crdt-agent-memory/internal/config"
)

const DefaultDimension = 8
const MaxDimension = 4096

type Config struct {
	Provider  string
	Model     string
	BaseURL   string
	APIKey    string
	Dimension int
	Timeout   time.Duration
}

type InputType string

const (
	InputTypeGeneric  InputType = "generic"
	InputTypeQuery    InputType = "query"
	InputTypeDocument InputType = "document"
)

type Provider interface {
	Embed(ctx context.Context, text string, inputType InputType) ([]float64, error)
}

var (
	providerMu    sync.Mutex
	providerOnce  sync.Once
	defaultProv   Provider
	defaultErr    error
	configuredCfg *Config
)

func Configure(cfg config.Embedding) {
	providerMu.Lock()
	defer providerMu.Unlock()
	timeout := time.Duration(cfg.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	configuredCfg = &Config{
		Provider:  strings.TrimSpace(cfg.Provider),
		Model:     strings.TrimSpace(cfg.Model),
		BaseURL:   strings.TrimSpace(cfg.BaseURL),
		Dimension: cfg.Dimension,
		Timeout:   timeout,
	}
	providerOnce = sync.Once{}
	defaultProv = nil
	defaultErr = nil
}

func CurrentConfig() Config {
	providerMu.Lock()
	defer providerMu.Unlock()
	return resolvedConfigLocked()
}

func FromText(ctx context.Context, text string) ([]float64, error) {
	return fromText(ctx, text, InputTypeGeneric)
}

func FromQuery(ctx context.Context, text string) ([]float64, error) {
	return fromText(ctx, text, InputTypeQuery)
}

func FromDocument(ctx context.Context, text string) ([]float64, error) {
	return fromText(ctx, text, InputTypeDocument)
}

func fromText(ctx context.Context, text string, inputType InputType) ([]float64, error) {
	prov, err := defaultProvider()
	if err != nil {
		return nil, err
	}
	return prov.Embed(ctx, text, inputType)
}

func LocalFromText(text string) []float64 {
	return localEmbed(text, DefaultDimension)
}

func defaultProvider() (Provider, error) {
	providerOnce.Do(func() {
		cfg := CurrentConfig()
		defaultProv, defaultErr = providerFromConfig(cfg)
	})
	return defaultProv, defaultErr
}

func providerFromConfig(cfg Config) (Provider, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case "", "local":
		return localProvider{dimension: normalizeDimension(cfg.Dimension)}, nil
	case "openai":
		return newOpenAIProvider(cfg)
	case "ruri-http":
		return newRuriHTTPProvider(cfg)
	default:
		return nil, fmt.Errorf("unsupported embedding provider %q", cfg.Provider)
	}
}

func resolvedConfigLocked() Config {
	cfg := Config{
		Provider:  "local",
		Dimension: DefaultDimension,
		Timeout:   3 * time.Second,
	}
	if configuredCfg != nil {
		cfg = *configuredCfg
		if cfg.Dimension <= 0 {
			cfg.Dimension = DefaultDimension
		}
		if cfg.Timeout <= 0 {
			cfg.Timeout = 3 * time.Second
		}
	}
	if raw := strings.TrimSpace(os.Getenv("EMBEDDING_PROVIDER")); raw != "" {
		cfg.Provider = raw
	}
	if raw := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); raw != "" {
		cfg.APIKey = raw
	}
	if raw := strings.TrimSpace(os.Getenv("OPENAI_EMBEDDING_MODEL")); raw != "" {
		cfg.Model = raw
	}
	if raw := strings.TrimSpace(os.Getenv("OPENAI_API_BASE")); raw != "" {
		cfg.BaseURL = raw
	}
	return cfg
}

func normalizeDimension(v int) int {
	if v <= 0 {
		return DefaultDimension
	}
	if v > MaxDimension {
		return MaxDimension
	}
	return v
}

type localProvider struct {
	dimension int
}

func (p localProvider) Embed(_ context.Context, text string, _ InputType) ([]float64, error) {
	return localEmbed(text, p.dimension), nil
}

func localEmbed(text string, dimension int) []float64 {
	dimension = normalizeDimension(dimension)
	tokens := tokenize(text)
	vec := make([]float64, dimension)
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

		vec[hashBucket(token, dimension)] += weight
		if i+1 < len(tokens) {
			bigram := token + " " + tokens[i+1]
			vec[hashBucket(bigram, dimension)] += 0.5
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

func hashBucket(token string, dimension int) int {
	dimension = normalizeDimension(dimension)
	h := fnv.New64a()
	_, _ = h.Write([]byte(token))
	bucket := 0
	for _, b := range h.Sum(nil) {
		bucket = ((bucket << 8) | int(b)) % dimension
	}
	return bucket
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
	dimension  int
	httpClient *http.Client
}

func newOpenAIProvider(cfg Config) (Provider, error) {
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		return nil, errors.New("OPENAI_API_KEY is required when embedding.provider=openai")
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = "text-embedding-3-small"
	}
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1/embeddings"
	}
	return openAIProvider{
		apiKey:    apiKey,
		model:     model,
		baseURL:   baseURL,
		dimension: normalizeDimension(cfg.Dimension),
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
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

func (p openAIProvider) Embed(ctx context.Context, text string, _ InputType) ([]float64, error) {
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
	vector := parsed.Data[0].Embedding
	if p.dimension > 0 && p.dimension != len(vector) {
		return compressEmbedding(vector, p.dimension), nil
	}
	return normalize(vector), nil
}

type ruriHTTPProvider struct {
	baseURL    string
	model      string
	dimension  int
	httpClient *http.Client
}

type ruriEmbeddingResponse struct {
	Embedding []float64 `json:"embedding"`
	Model     string    `json:"model"`
	Dimension int       `json:"dimension"`
	Error     string    `json:"error,omitempty"`
}

func newRuriHTTPProvider(cfg Config) (Provider, error) {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		return nil, errors.New("embedding.base_url is required when embedding.provider=ruri-http")
	}
	return ruriHTTPProvider{
		baseURL:   baseURL,
		model:     strings.TrimSpace(cfg.Model),
		dimension: normalizeDimension(cfg.Dimension),
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
	}, nil
}

func (p ruriHTTPProvider) Embed(ctx context.Context, text string, inputType InputType) ([]float64, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("embedding text is required")
	}
	raw, err := json.Marshal(map[string]string{
		"input":      text,
		"input_type": string(inputType),
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var parsed ruriEmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if parsed.Error != "" {
			return nil, fmt.Errorf("ruri embeddings api: %s", parsed.Error)
		}
		return nil, fmt.Errorf("ruri embeddings api: unexpected status %s", resp.Status)
	}
	if len(parsed.Embedding) == 0 {
		return nil, errors.New("ruri embeddings api returned no embedding data")
	}
	if parsed.Dimension > 0 && parsed.Dimension != len(parsed.Embedding) {
		return nil, fmt.Errorf("ruri embeddings api reported dimension %d but returned %d values", parsed.Dimension, len(parsed.Embedding))
	}
	if p.dimension > 0 && len(parsed.Embedding) != p.dimension {
		return nil, fmt.Errorf("ruri embeddings api returned dimension %d, want %d", len(parsed.Embedding), p.dimension)
	}
	if p.model != "" && parsed.Model != "" && parsed.Model != p.model {
		return nil, fmt.Errorf("ruri embeddings api returned model %q, want %q", parsed.Model, p.model)
	}
	return normalize(parsed.Embedding), nil
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
