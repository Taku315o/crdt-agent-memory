package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"crdt-agent-memory/internal/extensions"

	sqlite3 "github.com/mattn/go-sqlite3"
)

type OpenOptions struct {
	Path          string
	CRSQLitePath  string
	SQLiteVecPath string
}

var (
	driverMu      sync.Mutex
	registeredFor = map[string]string{}
)

func OpenSQLite(ctx context.Context, opts OpenOptions) (*sql.DB, error) {
	if strings.TrimSpace(opts.Path) == "" {
		return nil, errors.New("database path is required")
	}
	resolved, err := resolveExtensionPaths(opts)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(resolved.CRSQLitePath); err != nil {
		return nil, fmt.Errorf("crsqlite extension missing: %w", err)
	}
	if resolved.SQLiteVecPath != "" {
		if _, err := os.Stat(resolved.SQLiteVecPath); err != nil {
			return nil, fmt.Errorf("sqlite-vec extension missing: %w", err)
		}
	}
	driverName := registerDriver(resolved)
	if err := os.MkdirAll(filepath.Dir(opts.Path), 0o750); err != nil {
		return nil, err
	}
	db, err := sql.Open(driverName, opts.Path)
	if err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, `PRAGMA busy_timeout = 5000`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.ExecContext(ctx, `PRAGMA journal_mode = WAL`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.ExecContext(ctx, `PRAGMA trusted_schema = ON`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := db.QueryRowContext(ctx, `SELECT crsql_db_version()`).Scan(new(int64)); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("crsqlite extension not loaded correctly: %w", err)
	}
	if resolved.SQLiteVecPath != "" {
		if err := db.QueryRowContext(ctx, `SELECT vec_version()`).Scan(new(string)); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("sqlite-vec extension not loaded correctly: %w", err)
		}
	}
	return db, nil
}

func resolveExtensionPaths(opts OpenOptions) (OpenOptions, error) {
	resolved := opts
	if strings.TrimSpace(resolved.CRSQLitePath) == "" {
		path, ok, err := extensions.Resolve(extensions.NameCRSQLite)
		if err != nil {
			return OpenOptions{}, err
		}
		if !ok {
			return OpenOptions{}, errors.New("crsqlite extension path is required; no bundled extension is available for this platform")
		}
		resolved.CRSQLitePath = path
	}
	if strings.TrimSpace(resolved.SQLiteVecPath) == "" {
		path, ok, err := extensions.Resolve(extensions.NameSQLiteVec)
		if err != nil {
			return OpenOptions{}, err
		}
		if ok {
			resolved.SQLiteVecPath = path
		}
	}
	return resolved, nil
}

func registerDriver(opts OpenOptions) string {
	key := opts.CRSQLitePath + "|" + opts.SQLiteVecPath
	driverMu.Lock()
	defer driverMu.Unlock()
	if name, ok := registeredFor[key]; ok {
		return name
	}
	name := fmt.Sprintf("sqlite3_ext_%d", len(registeredFor)+1)
	sql.Register(name, &sqlite3.SQLiteDriver{
		ConnectHook: func(conn *sqlite3.SQLiteConn) error {
			if err := conn.LoadExtension(strings.TrimSuffix(opts.CRSQLitePath, filepath.Ext(opts.CRSQLitePath)), "sqlite3_crsqlite_init"); err != nil {
				return err
			}
			if opts.SQLiteVecPath != "" {
				if err := conn.LoadExtension(strings.TrimSuffix(opts.SQLiteVecPath, filepath.Ext(opts.SQLiteVecPath)), "sqlite3_vec_init"); err != nil {
					return err
				}
			}
			return nil
		},
	})
	registeredFor[key] = name
	return name
}
