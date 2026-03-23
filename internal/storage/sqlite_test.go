package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenSQLiteUsesBundledExtensionsWhenPathsAreOmitted(t *testing.T) {
	db, err := OpenSQLite(context.Background(), OpenOptions{
		Path: filepath.Join(t.TempDir(), "bundled.sqlite"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.QueryRowContext(context.Background(), `SELECT crsql_db_version()`).Scan(new(int64)); err != nil {
		t.Fatal(err)
	}
}
