package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"crdt-agent-memory/internal/config"
	"crdt-agent-memory/internal/embedding"
	"crdt-agent-memory/internal/indexer"
	"crdt-agent-memory/internal/ingest"
	"crdt-agent-memory/internal/memory"
	"crdt-agent-memory/internal/signing"
	"crdt-agent-memory/internal/storage"
)

type evalDataset struct {
	Documents []evalDocument `json:"documents"`
	Queries   []evalQuery    `json:"queries"`
}

type evalDocument struct {
	ID         string                  `json:"id"`
	Kind       string                  `json:"kind"`
	Visibility memory.Visibility       `json:"visibility"`
	Namespace  string                  `json:"namespace"`
	MemoryType string                  `json:"memory_type"`
	Subject    string                  `json:"subject"`
	Body       string                  `json:"body"`
	SessionID  string                  `json:"session_id"`
	Messages   []ingest.SessionMessage `json:"messages"`
}

type evalQuery struct {
	Query       string   `json:"query"`
	ExpectedIDs []string `json:"expected_ids"`
}

type evalResult struct {
	Query        string   `json:"query"`
	ExpectedIDs  []string `json:"expected_ids"`
	ReturnedIDs  []string `json:"returned_ids"`
	RecallAt5    bool     `json:"recall_at_5"`
	MRRAt10      float64  `json:"mrr_at_10"`
	WarningCount int      `json:"warning_count"`
}

type evalSummary struct {
	SearchProfile     string       `json:"search_profile"`
	FTSTokenizer      string       `json:"fts_tokenizer"`
	RankingProfile    string       `json:"ranking_profile"`
	EmbeddingProvider string       `json:"embedding_provider"`
	EmbeddingModel    string       `json:"embedding_model"`
	RecallAt5         float64      `json:"recall_at_5"`
	MRRAt10           float64      `json:"mrr_at_10"`
	Results           []evalResult `json:"results"`
}

func main() {
	var (
		datasetPath       string
		searchProfile     string
		ftsTokenizer      string
		rankingProfile    string
		embeddingProvider string
		embeddingModel    string
		embeddingBaseURL  string
		embeddingDim      int
		embeddingTimeout  int
	)
	flag.StringVar(&datasetPath, "dataset", "scripts/eval_dataset_ja.json", "path to evaluation dataset json")
	flag.StringVar(&searchProfile, "search-profile", "ja", "search profile name")
	flag.StringVar(&ftsTokenizer, "fts-tokenizer", "trigram", "fts tokenizer: unicode61 or trigram")
	flag.StringVar(&rankingProfile, "ranking-profile", "ja-default", "ranking profile name")
	flag.StringVar(&embeddingProvider, "embedding-provider", "local", "embedding provider")
	flag.StringVar(&embeddingModel, "embedding-model", "", "embedding model")
	flag.StringVar(&embeddingBaseURL, "embedding-base-url", "", "embedding api base url")
	flag.IntVar(&embeddingDim, "embedding-dimension", 8, "embedding dimension")
	flag.IntVar(&embeddingTimeout, "embedding-timeout-ms", 3000, "embedding timeout in ms")
	flag.Parse()

	ctx := context.Background()
	dataset, err := loadDataset(datasetPath)
	if err != nil {
		fail(err)
	}
	embedding.Configure(config.Embedding{
		Provider:  embeddingProvider,
		Model:     embeddingModel,
		BaseURL:   embeddingBaseURL,
		Dimension: embeddingDim,
		TimeoutMS: embeddingTimeout,
	})
	db, cleanup, err := openEvalDB(ctx)
	if err != nil {
		fail(err)
	}
	defer db.Close()
	defer cleanup()

	if _, err := storage.RunMigrationsWithOptions(ctx, db, storage.MigrationOptions{
		SearchProfile:  searchProfile,
		RankingProfile: rankingProfile,
		FTSTokenizer:   ftsTokenizer,
		EmbeddingDim:   embeddingDim,
	}); err != nil {
		fail(err)
	}

	signer, err := signing.NewSignerFromSeed(seedFor("eval-peer"))
	if err != nil {
		fail(err)
	}
	memSvc := memory.NewService(db, signer)
	ingestSvc := ingest.NewService(db)
	worker := indexer.NewWorker(db, 0)

	actualIDs := map[string]string{}
	for _, doc := range dataset.Documents {
		switch strings.ToLower(strings.TrimSpace(doc.Kind)) {
		case "memory":
			id, err := memSvc.Store(ctx, memory.StoreRequest{
				Visibility:    doc.Visibility,
				Namespace:     doc.Namespace,
				MemoryType:    doc.MemoryType,
				Subject:       doc.Subject,
				Body:          doc.Body,
				OriginPeerID:  "eval-peer",
				AuthorAgentID: "eval-agent",
			})
			if err != nil {
				fail(err)
			}
			actualIDs[doc.ID] = id
		case "transcript":
			if err := ingestSvc.IngestSession(ctx, ingest.SessionIngestRequest{
				SessionID:   doc.SessionID,
				SourceKind:  "eval",
				Namespace:   doc.Namespace,
				StartedAtMS: 100,
				EndedAtMS:   200,
				Messages:    doc.Messages,
			}); err != nil {
				fail(err)
			}
			chunkID, err := latestChunkID(ctx, db, doc.SessionID)
			if err != nil {
				fail(err)
			}
			actualIDs[doc.ID] = chunkID
		default:
			fail(fmt.Errorf("unsupported document kind %q", doc.Kind))
		}
	}

	for i := 0; i < 3; i++ {
		if err := worker.ProcessOnce(ctx); err != nil {
			fail(err)
		}
	}

	results := make([]evalResult, 0, len(dataset.Queries))
	var recallHits float64
	var mrrSum float64
	for _, query := range dataset.Queries {
		resp, err := memSvc.RecallDetailed(ctx, memory.RecallRequest{
			Query:             query.Query,
			IncludePrivate:    true,
			IncludeShared:     true,
			IncludeTranscript: true,
			Limit:             10,
		})
		if err != nil {
			fail(err)
		}
		expectedActual := make(map[string]struct{}, len(query.ExpectedIDs))
		for _, id := range query.ExpectedIDs {
			expectedActual[actualIDs[id]] = struct{}{}
		}
		returned := make([]string, 0, len(resp.Items))
		recallAt5 := false
		mrr := 0.0
		for i, item := range resp.Items {
			returned = append(returned, resolveDatasetID(actualIDs, item.UnitID))
			if _, ok := expectedActual[item.UnitID]; ok {
				if i < 5 {
					recallAt5 = true
				}
				if i < 10 && mrr == 0 {
					mrr = 1.0 / float64(i+1)
				}
			}
		}
		if recallAt5 {
			recallHits++
		}
		mrrSum += mrr
		results = append(results, evalResult{
			Query:        query.Query,
			ExpectedIDs:  query.ExpectedIDs,
			ReturnedIDs:  returned,
			RecallAt5:    recallAt5,
			MRRAt10:      mrr,
			WarningCount: len(resp.Warnings),
		})
	}

	summary := evalSummary{
		SearchProfile:     searchProfile,
		FTSTokenizer:      ftsTokenizer,
		RankingProfile:    rankingProfile,
		EmbeddingProvider: embeddingProvider,
		EmbeddingModel:    embeddingModel,
		RecallAt5:         recallHits / float64(len(dataset.Queries)),
		MRRAt10:           mrrSum / float64(len(dataset.Queries)),
		Results:           results,
	}
	raw, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		fail(err)
	}
	fmt.Println(string(raw))
}

func loadDataset(path string) (evalDataset, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return evalDataset{}, err
	}
	var dataset evalDataset
	if err := json.Unmarshal(raw, &dataset); err != nil {
		return evalDataset{}, err
	}
	return dataset, nil
}

func openEvalDB(ctx context.Context) (*sql.DB, func(), error) {
	dir, err := os.MkdirTemp("", "cam-eval-*")
	if err != nil {
		return nil, nil, err
	}
	db, err := storage.OpenSQLite(ctx, storage.OpenOptions{
		Path: filepath.Join(dir, "eval.sqlite"),
	})
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, nil, err
	}
	cleanup := func() {
		_ = os.RemoveAll(dir)
	}
	return db, cleanup, nil
}

func latestChunkID(ctx context.Context, db *sql.DB, sessionID string) (string, error) {
	var chunkID string
	err := db.QueryRowContext(ctx, `
		SELECT chunk_id
		FROM transcript_chunks
		WHERE session_id = ?
		ORDER BY chunk_strategy_version DESC, chunk_seq DESC
		LIMIT 1
	`, sessionID).Scan(&chunkID)
	return chunkID, err
}

func resolveDatasetID(actual map[string]string, returned string) string {
	for datasetID, actualID := range actual {
		if actualID == returned {
			return datasetID
		}
	}
	return returned
}

func seedFor(v string) []byte {
	sum := sha256.Sum256([]byte(v))
	return sum[:32]
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
