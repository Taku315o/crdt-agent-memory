package indexer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type Worker struct {
	db       *sql.DB
	interval time.Duration
}

func NewWorker(db *sql.DB, interval time.Duration) *Worker {
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	return &Worker{db: db, interval: interval}
}

type Diagnostics struct {
	ProcessedCount            int64 `json:"processed_count"`
	PendingCount              int64 `json:"pending_count"`
	EmbeddingCount            int64 `json:"embedding_count"`
	OldestPendingEnqueuedAtMS int64 `json:"oldest_pending_enqueued_at_ms"`
	OldestPendingAgeMS        int64 `json:"oldest_pending_age_ms"`
}

func (w *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		if err := w.ProcessOnce(ctx); err != nil {
			// Keep the daemon alive; item-level errors are retried on the next tick.
			// The caller can inspect diagnostics or logs for backlog.
			_ = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (w *Worker) ProcessOnce(ctx context.Context) error {
	items, err := w.pendingItems(ctx, 128)
	if err != nil {
		return err
	}
	var errs []error
	for _, it := range items {
		if err := w.processItem(ctx, it); err != nil {
			errs = append(errs, fmt.Errorf("%s/%s: %w", it.memorySpace, it.memoryID, err))
		}
	}
	return errors.Join(errs...)
}

func (w *Worker) Diagnostics(ctx context.Context) (Diagnostics, error) {
	var diag Diagnostics
	if err := w.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM index_queue WHERE processed_at_ms > 0
	`).Scan(&diag.ProcessedCount); err != nil {
		return Diagnostics{}, err
	}
	if err := w.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM index_queue WHERE processed_at_ms = 0
	`).Scan(&diag.PendingCount); err != nil {
		return Diagnostics{}, err
	}
	if err := w.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM memory_embeddings
	`).Scan(&diag.EmbeddingCount); err != nil {
		return Diagnostics{}, err
	}
	var oldest sql.NullInt64
	if err := w.db.QueryRowContext(ctx, `
		SELECT MIN(enqueued_at_ms) FROM index_queue WHERE processed_at_ms = 0
	`).Scan(&oldest); err != nil {
		return Diagnostics{}, err
	}
	if oldest.Valid {
		diag.OldestPendingEnqueuedAtMS = oldest.Int64
		diag.OldestPendingAgeMS = time.Now().UnixMilli() - oldest.Int64
	}
	return diag, nil
}

type queueItem struct {
	queueID     string
	memorySpace string
	memoryID    string
}

func (w *Worker) pendingItems(ctx context.Context, limit int) ([]queueItem, error) {
	if limit <= 0 {
		limit = 128
	}
	rows, err := w.db.QueryContext(ctx, `
		SELECT queue_id, memory_space, memory_id
		FROM index_queue
		WHERE processed_at_ms = 0
		ORDER BY enqueued_at_ms
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []queueItem
	for rows.Next() {
		var it queueItem
		if err := rows.Scan(&it.queueID, &it.memorySpace, &it.memoryID); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (w *Worker) processItem(ctx context.Context, item queueItem) error {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	body, found, err := fetchBody(ctx, tx, item.memorySpace, item.memoryID)
	if err != nil {
		return err
	}
	if !found {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM memory_embeddings WHERE memory_space = ? AND memory_id = ?
		`, item.memorySpace, item.memoryID); err != nil {
			return err
		}
		return finishQueueItem(ctx, tx, item.queueID)
	}
	if err := upsertEmbedding(ctx, tx, item.memorySpace, item.memoryID, body); err != nil {
		return err
	}
	return finishQueueItem(ctx, tx, item.queueID)
}

func fetchBody(ctx context.Context, tx *sql.Tx, memorySpace, memoryID string) (string, bool, error) {
	var body string
	switch memorySpace {
	case "shared":
		if err := tx.QueryRowContext(ctx, `SELECT body FROM memory_nodes WHERE memory_id = ?`, memoryID).Scan(&body); err != nil {
			if err == sql.ErrNoRows {
				return "", false, nil
			}
			return "", false, err
		}
		return body, true, nil
	case "private":
		if err := tx.QueryRowContext(ctx, `SELECT body FROM private_memory_nodes WHERE memory_id = ?`, memoryID).Scan(&body); err != nil {
			if err == sql.ErrNoRows {
				return "", false, nil
			}
			return "", false, err
		}
		return body, true, nil
	default:
		return "", false, nil
	}
}

func upsertEmbedding(ctx context.Context, tx *sql.Tx, memorySpace, memoryID, body string) error {
	vector := embed(body)
	raw, err := json.Marshal(vector)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO memory_embeddings(memory_space, memory_id, embedding_json, embedding_dim, indexed_at_ms)
		VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(memory_space, memory_id) DO UPDATE SET
			embedding_json = excluded.embedding_json,
			embedding_dim = excluded.embedding_dim,
			indexed_at_ms = excluded.indexed_at_ms
	`, memorySpace, memoryID, string(raw), len(vector), time.Now().UnixMilli())
	return err
}

func finishQueueItem(ctx context.Context, tx *sql.Tx, queueID string) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE index_queue SET processed_at_ms = ? WHERE queue_id = ?
	`, time.Now().UnixMilli(), queueID); err != nil {
		return err
	}
	return tx.Commit()
}

func embed(body string) []float64 {
	sum := sha256.Sum256([]byte(body))
	out := make([]float64, 8)
	for i := 0; i < len(out); i++ {
		segment := binary.BigEndian.Uint32(sum[i*4 : i*4+4])
		out[i] = float64(segment%1000) / 1000.0
	}
	return out
}
