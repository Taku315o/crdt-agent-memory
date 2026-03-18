package indexer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
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

func (w *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		if err := w.ProcessOnce(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (w *Worker) ProcessOnce(ctx context.Context) error {
	rows, err := w.db.QueryContext(ctx, `
		SELECT queue_id, memory_space, memory_id
		FROM index_queue
		WHERE processed_at_ms = 0
		ORDER BY enqueued_at_ms
		LIMIT 128
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type item struct {
		queueID     string
		memorySpace string
		memoryID    string
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.queueID, &it.memorySpace, &it.memoryID); err != nil {
			return err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, it := range items {
		if err := w.indexMemory(ctx, it.memorySpace, it.memoryID); err != nil {
			return err
		}
		if _, err := w.db.ExecContext(ctx, `
			UPDATE index_queue SET processed_at_ms = ? WHERE queue_id = ?
		`, time.Now().UnixMilli(), it.queueID); err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) indexMemory(ctx context.Context, memorySpace, memoryID string) error {
	var body string
	switch memorySpace {
	case "shared":
		if err := w.db.QueryRowContext(ctx, `SELECT body FROM memory_nodes WHERE memory_id = ?`, memoryID).Scan(&body); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return err
		}
	case "private":
		if err := w.db.QueryRowContext(ctx, `SELECT body FROM private_memory_nodes WHERE memory_id = ?`, memoryID).Scan(&body); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return err
		}
	default:
		return nil
	}
	vector := embed(body)
	raw, err := json.Marshal(vector)
	if err != nil {
		return err
	}
	_, err = w.db.ExecContext(ctx, `
		INSERT INTO memory_embeddings(memory_space, memory_id, embedding_json, embedding_dim, indexed_at_ms)
		VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(memory_space, memory_id) DO UPDATE SET
			embedding_json = excluded.embedding_json,
			embedding_dim = excluded.embedding_dim,
			indexed_at_ms = excluded.indexed_at_ms
	`, memorySpace, memoryID, string(raw), len(vector), time.Now().UnixMilli())
	return err
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
