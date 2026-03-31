package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

var (
	sharedCRRTables = []string{"artifact_refs", "artifact_spans", "memory_edges", "memory_nodes", "memory_signals"}
	ErrLegacyDB     = errors.New("legacy fake-crr database detected; recreate the database from scratch")
)

type Metadata struct {
	SchemaHash                   string
	CRRManifestHash              string
	ProtocolVersion              string
	MinCompatibleProtocolVersion string
}

type MigrationOptions struct {
	SearchProfile     string
	RankingProfile    string
	FTSTokenizer      string
	EmbeddingDim      int
	ForceRebuildIndex bool
}

func RunMigrations(ctx context.Context, db *sql.DB) (Metadata, error) {
	return RunMigrationsWithOptions(ctx, db, MigrationOptions{})
}

func RunMigrationsWithOptions(ctx context.Context, db *sql.DB, opts MigrationOptions) (Metadata, error) {
	if err := detectLegacyDB(ctx, db); err != nil {
		return Metadata{}, err
	}
	opts = normalizeMigrationOptions(opts)
	migrationsDir := migrationDir()
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return Metadata{}, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

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
	appliedNewMigration := false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version := entry.Name()
		var alreadyApplied string
		err := tx.QueryRowContext(ctx, `SELECT version FROM schema_migrations WHERE version = ?`, version).Scan(&alreadyApplied)
		if err == nil {
			// #nosec G304 -- migration filenames come from os.ReadDir on the fixed migrations directory.
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

		// #nosec G304 -- migration filenames come from os.ReadDir on the fixed migrations directory.
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
		appliedNewMigration = true
	}

	var crrEnabled string
	err = tx.QueryRowContext(ctx, `SELECT value FROM app_metadata WHERE key = 'crr_enabled'`).Scan(&crrEnabled)
	if err != nil && err != sql.ErrNoRows {
		return Metadata{}, err
	}
	if err == sql.ErrNoRows || appliedNewMigration {
		for _, table := range sharedCRRTables {
			var ignored any
			if err := tx.QueryRowContext(ctx, `SELECT crsql_as_crr(?)`, table).Scan(&ignored); err != nil && !strings.Contains(err.Error(), "already") {
				return Metadata{}, fmt.Errorf("enable crr for %s: %w", table, err)
			}
		}
		if err := ensureSharedTriggers(ctx, tx); err != nil {
			return Metadata{}, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO app_metadata(key, value) VALUES('crr_enabled', '1')
			ON CONFLICT(key) DO UPDATE SET value = excluded.value
		`); err != nil {
			return Metadata{}, err
		}
	}

	vecEnabled, err := hasSQLiteVec(ctx, tx)
	if err != nil {
		return Metadata{}, err
	}
	ftsEnabled, err := hasFTS5(ctx, tx)
	if err != nil {
		return Metadata{}, err
	}
	if ftsEnabled {
		rebuildFTS, err := shouldBackfillFTSIndexes(ctx, tx, appliedNewMigration, opts.FTSTokenizer, opts.ForceRebuildIndex)
		if err != nil {
			return Metadata{}, err
		}
		if err := ensureFTSIndexes(ctx, tx, rebuildFTS, opts.FTSTokenizer); err != nil {
			return Metadata{}, err
		}
	}
	if vecEnabled {
		rebuildVec, err := shouldRebuildVectorIndexes(ctx, tx, appliedNewMigration, opts.EmbeddingDim, opts.ForceRebuildIndex)
		if err != nil {
			return Metadata{}, err
		}
		if rebuildVec {
			if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS memory_embedding_vectors`); err != nil {
				return Metadata{}, err
			}
			if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS retrieval_embedding_vectors`); err != nil {
				return Metadata{}, err
			}
		}
		vecDDL := vectorTableDDL("memory_embedding_vectors", "memory_id", opts.EmbeddingDim)
		// #nosec G202 -- dimension is range-checked in normalizeMigrationOptions and interpolated into fixed DDL.
		if _, err := tx.ExecContext(ctx, vecDDL); err != nil {
			return Metadata{}, fmt.Errorf("create vector index: %w", err)
		}
		retrievalVecDDL := vectorTableDDL("retrieval_embedding_vectors", "unit_id", opts.EmbeddingDim)
		// #nosec G202 -- dimension is range-checked in normalizeMigrationOptions and interpolated into fixed DDL.
		if _, err := tx.ExecContext(ctx, retrievalVecDDL); err != nil {
			return Metadata{}, fmt.Errorf("create retrieval vector index: %w", err)
		}
	}

	meta := Metadata{
		SchemaHash:                   hashString(combined.String()),
		CRRManifestHash:              hashString(strings.Join(sharedCRRTables, ",")),
		ProtocolVersion:              "1",
		MinCompatibleProtocolVersion: "1",
	}

	for key, value := range map[string]string{
		"schema_hash":                     meta.SchemaHash,
		"crr_manifest_hash":               meta.CRRManifestHash,
		"protocol_version":                meta.ProtocolVersion,
		"min_compatible_protocol_version": meta.MinCompatibleProtocolVersion,
		"fts5_enabled":                    boolString(ftsEnabled),
		"search_profile":                  opts.SearchProfile,
		"ranking_profile":                 opts.RankingProfile,
		"fts_tokenizer":                   opts.FTSTokenizer,
		"embedding_dimension":             fmt.Sprintf("%d", opts.EmbeddingDim),
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

func normalizeMigrationOptions(opts MigrationOptions) MigrationOptions {
	if strings.TrimSpace(opts.FTSTokenizer) == "" {
		opts.FTSTokenizer = "unicode61"
	}
	if strings.TrimSpace(opts.SearchProfile) == "" {
		opts.SearchProfile = "default"
	}
	if strings.TrimSpace(opts.RankingProfile) == "" {
		opts.RankingProfile = opts.SearchProfile
	}
	if opts.EmbeddingDim <= 0 {
		opts.EmbeddingDim = 8
	}
	if opts.EmbeddingDim > 4096 {
		opts.EmbeddingDim = 4096
	}
	return opts
}

func vectorTableDDL(tableName, idColumn string, dim int) string {
	// #nosec G202 -- caller only passes fixed table/column names and a range-checked dimension.
	return fmt.Sprintf(`
			CREATE VIRTUAL TABLE IF NOT EXISTS %s USING vec0(
				memory_space TEXT PARTITION KEY,
				%s TEXT,
				embedding FLOAT[%d]
			)
		`, tableName, idColumn, dim)
}

func hasFTS5(ctx context.Context, tx *sql.Tx) (bool, error) {
	if _, err := tx.ExecContext(ctx, `CREATE VIRTUAL TABLE temp.codex_fts5_probe USING fts5(body)`); err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "no such module") && strings.Contains(msg, "fts5") {
			return false, nil
		}
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE temp.codex_fts5_probe`); err != nil {
		return false, err
	}
	return true, nil
}

func hasSQLiteVec(ctx context.Context, tx *sql.Tx) (bool, error) {
	var version string
	if err := tx.QueryRowContext(ctx, `SELECT vec_version()`).Scan(&version); err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "no such function") && strings.Contains(msg, "vec_version") {
			return false, nil
		}
		return false, err
	}
	return version != "", nil
}

func shouldBackfillFTSIndexes(ctx context.Context, tx *sql.Tx, appliedNewMigration bool, tokenizer string, force bool) (bool, error) {
	if appliedNewMigration || force {
		return true, nil
	}
	var existing int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM sqlite_master
		WHERE type = 'table'
		  AND name IN ('memory_fts_index', 'retrieval_fts_index')
	`).Scan(&existing); err != nil {
		return false, err
	}
	if existing < 2 {
		return true, nil
	}
	storedTokenizer, err := metadataValue(ctx, tx, "fts_tokenizer")
	if err != nil {
		return false, err
	}
	if storedTokenizer == "" {
		// Older databases predate the fts_tokenizer metadata key and therefore
		// implicitly used the historical default tokenizer.
		return tokenizer != "unicode61", nil
	}
	return storedTokenizer != tokenizer, nil
}

func ensureFTSIndexes(ctx context.Context, tx *sql.Tx, rebuild bool, tokenizer string) error {
	tokenizerDDL, err := ftsTokenizerDDL(tokenizer)
	if err != nil {
		return err
	}
	if rebuild {
		if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS memory_fts_index`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS retrieval_fts_index`); err != nil {
			return err
		}
	}
	statements := []string{
		`CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts_index USING fts5(
			memory_space UNINDEXED,
			memory_id UNINDEXED,
			namespace UNINDEXED,
			subject,
			body
		, tokenize='` + tokenizerDDL + `')`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS retrieval_fts_index USING fts5(
			unit_id UNINDEXED,
			memory_space UNINDEXED,
			namespace UNINDEXED,
			source_type UNINDEXED,
			unit_kind UNINDEXED,
			title,
			body
		, tokenize='` + tokenizerDDL + `')`,
		`CREATE TRIGGER IF NOT EXISTS trg_memory_fts_index_insert
		AFTER INSERT ON memory_fts
		BEGIN
			INSERT INTO memory_fts_index(memory_space, memory_id, namespace, subject, body)
			VALUES (NEW.memory_space, NEW.memory_id, NEW.namespace, NEW.subject, NEW.body);
		END;`,
		`CREATE TRIGGER IF NOT EXISTS trg_memory_fts_index_update
		AFTER UPDATE ON memory_fts
		BEGIN
			DELETE FROM memory_fts_index WHERE memory_space = OLD.memory_space AND memory_id = OLD.memory_id;
			INSERT INTO memory_fts_index(memory_space, memory_id, namespace, subject, body)
			VALUES (NEW.memory_space, NEW.memory_id, NEW.namespace, NEW.subject, NEW.body);
		END;`,
		`CREATE TRIGGER IF NOT EXISTS trg_memory_fts_index_delete
		AFTER DELETE ON memory_fts
		BEGIN
			DELETE FROM memory_fts_index WHERE memory_space = OLD.memory_space AND memory_id = OLD.memory_id;
		END;`,
		`CREATE TRIGGER IF NOT EXISTS trg_retrieval_fts_index_insert
		AFTER INSERT ON retrieval_fts
		BEGIN
			INSERT INTO retrieval_fts_index(unit_id, memory_space, namespace, source_type, unit_kind, title, body)
			VALUES (NEW.unit_id, NEW.memory_space, NEW.namespace, NEW.source_type, NEW.unit_kind, NEW.title, NEW.body);
		END;`,
		`CREATE TRIGGER IF NOT EXISTS trg_retrieval_fts_index_update
		AFTER UPDATE ON retrieval_fts
		BEGIN
			DELETE FROM retrieval_fts_index WHERE unit_id = OLD.unit_id;
			INSERT INTO retrieval_fts_index(unit_id, memory_space, namespace, source_type, unit_kind, title, body)
			VALUES (NEW.unit_id, NEW.memory_space, NEW.namespace, NEW.source_type, NEW.unit_kind, NEW.title, NEW.body);
		END;`,
		`CREATE TRIGGER IF NOT EXISTS trg_retrieval_fts_index_delete
		AFTER DELETE ON retrieval_fts
		BEGIN
			DELETE FROM retrieval_fts_index WHERE unit_id = OLD.unit_id;
		END;`,
	}
	for _, stmt := range statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	if !rebuild {
		return nil
	}
	for _, stmt := range []string{
		`DELETE FROM memory_fts_index`,
		`INSERT INTO memory_fts_index(memory_space, memory_id, namespace, subject, body)
		SELECT memory_space, memory_id, namespace, subject, body FROM memory_fts`,
		`DELETE FROM retrieval_fts_index`,
		`INSERT INTO retrieval_fts_index(unit_id, memory_space, namespace, source_type, unit_kind, title, body)
		SELECT unit_id, memory_space, namespace, source_type, unit_kind, title, body FROM retrieval_fts`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func shouldRebuildVectorIndexes(ctx context.Context, tx *sql.Tx, appliedNewMigration bool, embeddingDim int, force bool) (bool, error) {
	if appliedNewMigration || force {
		return true, nil
	}
	storedDim, err := metadataValue(ctx, tx, "embedding_dimension")
	if err != nil {
		return false, err
	}
	if storedDim == "" {
		return false, nil
	}
	return storedDim != fmt.Sprintf("%d", embeddingDim), nil
}

func metadataValue(ctx context.Context, tx *sql.Tx, key string) (string, error) {
	var value string
	err := tx.QueryRowContext(ctx, `SELECT value FROM app_metadata WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "no such table") && strings.Contains(msg, "app_metadata") {
			return "", nil
		}
	}
	return value, err
}

func ftsTokenizerDDL(tokenizer string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(tokenizer)) {
	case "", "unicode61":
		return "unicode61", nil
	case "trigram":
		return "trigram", nil
	default:
		return "", fmt.Errorf("unsupported fts tokenizer %q", tokenizer)
	}
}

func boolString(v bool) string {
	if v {
		return "1"
	}
	return "0"
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
		var value string
		if err := db.QueryRowContext(ctx, `SELECT value FROM app_metadata WHERE key = ?`, key).Scan(&value); err != nil {
			return Metadata{}, err
		}
		values[key] = value
	}
	return Metadata{
		SchemaHash:                   values["schema_hash"],
		CRRManifestHash:              values["crr_manifest_hash"],
		ProtocolVersion:              values["protocol_version"],
		MinCompatibleProtocolVersion: values["min_compatible_protocol_version"],
	}, nil
}

func detectLegacyDB(ctx context.Context, db *sql.DB) error {
	for _, table := range []string{"crsql_clock", "capture_control"} {
		var count int
		if err := db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?
		`, table).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return ErrLegacyDB
		}
	}
	return nil
}

func hashString(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])
}

func ensureSharedTriggers(ctx context.Context, tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE TRIGGER IF NOT EXISTS trg_memory_nodes_sync_insert AFTER INSERT ON memory_nodes BEGIN
			INSERT INTO sync_change_log(db_version, table_name, pk_hint, namespace, memory_id, changed_at_ms)
			VALUES(crsql_db_version() + 1, 'memory_nodes', NEW.memory_id, NEW.namespace, NEW.memory_id, NEW.authored_at_ms);
		END;`,
		`CREATE TRIGGER IF NOT EXISTS trg_memory_nodes_sync_update AFTER UPDATE ON memory_nodes BEGIN
			INSERT INTO sync_change_log(db_version, table_name, pk_hint, namespace, memory_id, changed_at_ms)
			VALUES(crsql_db_version() + 1, 'memory_nodes', NEW.memory_id, NEW.namespace, NEW.memory_id, strftime('%s','now') * 1000);
		END;`,
		`CREATE TRIGGER IF NOT EXISTS trg_memory_edges_sync_insert AFTER INSERT ON memory_edges BEGIN
			INSERT INTO sync_change_log(db_version, table_name, pk_hint, namespace, memory_id, changed_at_ms)
			VALUES(crsql_db_version() + 1, 'memory_edges', NEW.edge_id, COALESCE((SELECT namespace FROM memory_nodes WHERE memory_id = NEW.from_memory_id), (SELECT namespace FROM memory_nodes WHERE memory_id = NEW.to_memory_id), ''), COALESCE(NEW.from_memory_id, NEW.to_memory_id, ''), NEW.authored_at_ms);
		END;`,
		`CREATE TRIGGER IF NOT EXISTS trg_memory_edges_sync_update AFTER UPDATE ON memory_edges BEGIN
			INSERT INTO sync_change_log(db_version, table_name, pk_hint, namespace, memory_id, changed_at_ms)
			VALUES(crsql_db_version() + 1, 'memory_edges', NEW.edge_id, COALESCE((SELECT namespace FROM memory_nodes WHERE memory_id = NEW.from_memory_id), (SELECT namespace FROM memory_nodes WHERE memory_id = NEW.to_memory_id), ''), COALESCE(NEW.from_memory_id, NEW.to_memory_id, ''), strftime('%s','now') * 1000);
		END;`,
		`CREATE TRIGGER IF NOT EXISTS trg_memory_signals_sync_insert AFTER INSERT ON memory_signals BEGIN
			INSERT INTO sync_change_log(db_version, table_name, pk_hint, namespace, memory_id, changed_at_ms)
			VALUES(crsql_db_version() + 1, 'memory_signals', NEW.signal_id, COALESCE((SELECT namespace FROM memory_nodes WHERE memory_id = NEW.memory_id), ''), NEW.memory_id, NEW.authored_at_ms);
		END;`,
		`CREATE TRIGGER IF NOT EXISTS trg_memory_signals_sync_update AFTER UPDATE ON memory_signals BEGIN
			INSERT INTO sync_change_log(db_version, table_name, pk_hint, namespace, memory_id, changed_at_ms)
			VALUES(crsql_db_version() + 1, 'memory_signals', NEW.signal_id, COALESCE((SELECT namespace FROM memory_nodes WHERE memory_id = NEW.memory_id), ''), NEW.memory_id, strftime('%s','now') * 1000);
		END;`,
		`CREATE TRIGGER IF NOT EXISTS trg_artifact_refs_sync_insert AFTER INSERT ON artifact_refs BEGIN
			INSERT INTO sync_change_log(db_version, table_name, pk_hint, namespace, memory_id, changed_at_ms)
			VALUES(crsql_db_version() + 1, 'artifact_refs', NEW.artifact_id, NEW.namespace, '', NEW.authored_at_ms);
		END;`,
		`CREATE TRIGGER IF NOT EXISTS trg_artifact_refs_sync_update AFTER UPDATE ON artifact_refs BEGIN
			INSERT INTO sync_change_log(db_version, table_name, pk_hint, namespace, memory_id, changed_at_ms)
			VALUES(crsql_db_version() + 1, 'artifact_refs', NEW.artifact_id, NEW.namespace, '', strftime('%s','now') * 1000);
		END;`,
		`CREATE TRIGGER IF NOT EXISTS trg_artifact_spans_sync_insert AFTER INSERT ON artifact_spans BEGIN
			INSERT INTO sync_change_log(db_version, table_name, pk_hint, namespace, memory_id, changed_at_ms)
			VALUES(
				crsql_db_version() + 1,
				'artifact_spans',
				NEW.span_id,
				COALESCE((SELECT namespace FROM memory_nodes WHERE memory_id = NEW.memory_id), (SELECT namespace FROM artifact_refs WHERE artifact_id = NEW.artifact_id), ''),
				NEW.memory_id,
				NEW.authored_at_ms
			);
		END;`,
		`CREATE TRIGGER IF NOT EXISTS trg_artifact_spans_sync_update AFTER UPDATE ON artifact_spans BEGIN
			INSERT INTO sync_change_log(db_version, table_name, pk_hint, namespace, memory_id, changed_at_ms)
			VALUES(
				crsql_db_version() + 1,
				'artifact_spans',
				NEW.span_id,
				COALESCE((SELECT namespace FROM memory_nodes WHERE memory_id = NEW.memory_id), (SELECT namespace FROM artifact_refs WHERE artifact_id = NEW.artifact_id), ''),
				NEW.memory_id,
				strftime('%s','now') * 1000
			);
		END;`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func migrationDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..", "migrations")
}
