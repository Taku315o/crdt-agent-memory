package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

var sharedCRRTables = []string{"artifact_refs", "memory_edges", "memory_nodes", "memory_signals"}

type Metadata struct {
	SchemaHash                string
	CRRManifestHash           string
	ProtocolVersion           string
	MinCompatibleProtocolVersion string
}

func RunMigrations(ctx context.Context, db *sql.DB) (Metadata, error) {
	migrationsDir := migrationDir()
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return Metadata{}, err
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Metadata{}, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at_ms INTEGER NOT NULL
		)
	`); err != nil {
		return Metadata{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS app_metadata (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)
	`); err != nil {
		return Metadata{}, err
	}

	combined := strings.Builder{}
	now := time.Now().UnixMilli()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version := entry.Name()
		var alreadyApplied string
		err := tx.QueryRowContext(ctx, `SELECT version FROM schema_migrations WHERE version = ?`, version).Scan(&alreadyApplied)
		if err == nil {
			content, readErr := os.ReadFile(filepath.Join(migrationsDir, version))
			if readErr != nil {
				return Metadata{}, readErr
			}
			combined.Write(content)
			continue
		}
		if err != sql.ErrNoRows {
			return Metadata{}, err
		}

		content, err := os.ReadFile(filepath.Join(migrationsDir, version))
		if err != nil {
			return Metadata{}, err
		}
		if _, err := tx.ExecContext(ctx, string(content)); err != nil {
			return Metadata{}, fmt.Errorf("apply migration %s: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at_ms) VALUES(?, ?)`, version, now); err != nil {
			return Metadata{}, err
		}
		combined.Write(content)
	}

	meta := Metadata{
		SchemaHash:                   hashString(combined.String()),
		CRRManifestHash:              hashString(strings.Join(sharedCRRTables, ",")),
		ProtocolVersion:              "1",
		MinCompatibleProtocolVersion: "1",
	}

	for key, value := range map[string]string{
		"schema_hash":                    meta.SchemaHash,
		"crr_manifest_hash":              meta.CRRManifestHash,
		"protocol_version":               meta.ProtocolVersion,
		"min_compatible_protocol_version": meta.MinCompatibleProtocolVersion,
	} {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO app_metadata(key, value) VALUES(?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value
		`, key, value); err != nil {
			return Metadata{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return Metadata{}, err
	}
	return meta, nil
}

func LoadMetadata(ctx context.Context, db *sql.DB) (Metadata, error) {
	keys := []string{
		"schema_hash",
		"crr_manifest_hash",
		"protocol_version",
		"min_compatible_protocol_version",
	}
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		if err := db.QueryRowContext(ctx, `SELECT value FROM app_metadata WHERE key = ?`, key).Scan(&values[key]); err != nil {
			return Metadata{}, err
		}
	}
	return Metadata{
		SchemaHash:                   values["schema_hash"],
		CRRManifestHash:              values["crr_manifest_hash"],
		ProtocolVersion:              values["protocol_version"],
		MinCompatibleProtocolVersion: values["min_compatible_protocol_version"],
	}, nil
}

func hashString(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])
}

func migrationDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..", "migrations")
}
