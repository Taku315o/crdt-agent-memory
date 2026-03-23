package extensions

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

//go:embed assets/*
var assets embed.FS

const (
	NameCRSQLite  = "crsqlite"
	NameSQLiteVec = "sqlite-vec"
)

var writeMu sync.Mutex

func Resolve(name string) (string, bool, error) {
	fileName, entry, err := assetSpec(name)
	if err != nil {
		return "", false, err
	}
	raw, err := assets.ReadFile(entry)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	sum := sha256.Sum256(raw)
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", false, err
	}
	targetDir := filepath.Join(cacheDir, "crdt-agent-memory", "extensions", runtime.GOOS+"-"+runtime.GOARCH)
	targetPath := filepath.Join(targetDir, fmt.Sprintf("%s-%x%s", fileStem(fileName), sum[:8], filepath.Ext(fileName)))

	writeMu.Lock()
	defer writeMu.Unlock()

	if _, err := os.Stat(targetPath); err == nil {
		return targetPath, true, nil
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", false, err
	}
	if err := os.WriteFile(targetPath, raw, 0o755); err != nil {
		return "", false, err
	}
	return targetPath, true, nil
}

func assetSpec(name string) (fileName string, entry string, err error) {
	platform := runtime.GOOS + "-" + runtime.GOARCH
	switch name {
	case NameCRSQLite:
		fileName = dylibName("crsqlite")
	case NameSQLiteVec:
		fileName = dylibName("vec0")
	default:
		return "", "", fmt.Errorf("unknown extension: %s", name)
	}
	return fileName, filepath.Join("assets", platform, fileName), nil
}

func dylibName(base string) string {
	switch runtime.GOOS {
	case "darwin":
		return base + ".dylib"
	case "linux":
		return base + ".so"
	case "windows":
		return base + ".dll"
	default:
		return base
	}
}

func fileStem(name string) string {
	ext := filepath.Ext(name)
	return name[:len(name)-len(ext)]
}
