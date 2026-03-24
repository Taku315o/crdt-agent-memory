package testenv

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func CRSQLitePath(t *testing.T) string {
	t.Helper()
	for _, candidate := range []string{
		os.Getenv("CRSQLITE_PATH"),
		filepath.Join(repoRoot(t), ".tools", "crsqlite", "crsqlite.dylib"),
		"/tmp/crsqlite-check/crsqlite.dylib",
	} {
		if candidate == "" {
			continue
		}
		// #nosec G304,G703 -- test helper paths are from controlled env vars or repo-local fixtures.
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	t.Fatalf("crsqlite extension not found; run make bootstrap-dev or set CRSQLITE_PATH")
	return ""
}

func SQLiteVecPath() string {
	for _, candidate := range []string{
		os.Getenv("SQLITE_VEC_PATH"),
		filepath.Join(repoRootNoTest(), ".tools", "sqlite-vec", "vec0.dylib"),
	} {
		if candidate == "" {
			continue
		}
		// #nosec G304,G703 -- test helper paths are from controlled env vars or repo-local fixtures.
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func repoRoot(t *testing.T) string {
	t.Helper()
	return repoRootNoTest()
}

func repoRootNoTest() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..")
}
