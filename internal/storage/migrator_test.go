package storage

import (
	"context"
	"database/sql"
	"testing"
)

func TestShouldBackfillFTSIndexes(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	needBackfill, err := shouldBackfillFTSIndexes(ctx, tx, false, "unicode61", false)
	if err != nil {
		t.Fatal(err)
	}
	if !needBackfill {
		t.Fatal("needBackfill = false, want true when tables are missing")
	}

	if _, err := tx.ExecContext(ctx, `CREATE TABLE memory_fts_index(id TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE retrieval_fts_index(id TEXT)`); err != nil {
		t.Fatal(err)
	}

	needBackfill, err = shouldBackfillFTSIndexes(ctx, tx, false, "unicode61", false)
	if err != nil {
		t.Fatal(err)
	}
	if needBackfill {
		t.Fatal("needBackfill = true, want false when both tables already exist")
	}

	needBackfill, err = shouldBackfillFTSIndexes(ctx, tx, true, "unicode61", false)
	if err != nil {
		t.Fatal(err)
	}
	if !needBackfill {
		t.Fatal("needBackfill = false, want true when a new migration was applied")
	}
}
