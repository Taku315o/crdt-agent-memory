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
		t.Fatal("needBackfill = true, want false when both tables already exist with the historical default tokenizer")
	}

	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE app_metadata (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO app_metadata(key, value) VALUES('fts_tokenizer', 'trigram')`); err != nil {
		t.Fatal(err)
	}

	needBackfill, err = shouldBackfillFTSIndexes(ctx, tx, false, "unicode61", false)
	if err != nil {
		t.Fatal(err)
	}
	if !needBackfill {
		t.Fatal("needBackfill = false, want true when stored tokenizer differs from the configured tokenizer")
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM app_metadata WHERE key = 'fts_tokenizer'`); err != nil {
		t.Fatal(err)
	}

	needBackfill, err = shouldBackfillFTSIndexes(ctx, tx, false, "trigram", false)
	if err != nil {
		t.Fatal(err)
	}
	if !needBackfill {
		t.Fatal("needBackfill = false, want true when tokenizer metadata is missing and config differs from the historical default")
	}

	needBackfill, err = shouldBackfillFTSIndexes(ctx, tx, true, "unicode61", false)
	if err != nil {
		t.Fatal(err)
	}
	if !needBackfill {
		t.Fatal("needBackfill = false, want true when a new migration was applied")
	}
}

func TestShouldRebuildVectorIndexes(t *testing.T) {
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

	needRebuild, err := shouldRebuildVectorIndexes(ctx, tx, false, 8, false)
	if err != nil {
		t.Fatal(err)
	}
	if needRebuild {
		t.Fatal("needRebuild = true, want false when metadata and vector tables are both absent")
	}

	if _, err := tx.ExecContext(ctx, `CREATE TABLE memory_embedding_vectors(id TEXT)`); err != nil {
		t.Fatal(err)
	}

	needRebuild, err = shouldRebuildVectorIndexes(ctx, tx, false, 8, false)
	if err != nil {
		t.Fatal(err)
	}
	if !needRebuild {
		t.Fatal("needRebuild = false, want true when vector tables exist but embedding_dimension metadata is missing")
	}

	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE app_metadata (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO app_metadata(key, value) VALUES('embedding_dimension', '8')`); err != nil {
		t.Fatal(err)
	}

	needRebuild, err = shouldRebuildVectorIndexes(ctx, tx, false, 8, false)
	if err != nil {
		t.Fatal(err)
	}
	if needRebuild {
		t.Fatal("needRebuild = true, want false when stored embedding dimension matches config")
	}

	needRebuild, err = shouldRebuildVectorIndexes(ctx, tx, false, 16, false)
	if err != nil {
		t.Fatal(err)
	}
	if !needRebuild {
		t.Fatal("needRebuild = false, want true when stored embedding dimension differs from config")
	}

	needRebuild, err = shouldRebuildVectorIndexes(ctx, tx, true, 8, false)
	if err != nil {
		t.Fatal(err)
	}
	if !needRebuild {
		t.Fatal("needRebuild = false, want true when a new migration was applied")
	}
}
