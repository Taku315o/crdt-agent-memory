package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"time"

	"crdt-agent-memory/internal/config"
	"crdt-agent-memory/internal/indexer"
	"crdt-agent-memory/internal/storage"
)

func main() {
	var configPath string
	var once bool
	var diag bool
	flag.StringVar(&configPath, "config", "", "path to config yaml")
	flag.BoolVar(&once, "once", false, "process queued items once")
	flag.BoolVar(&diag, "diag", false, "print queue diagnostics and exit")
	flag.Parse()
	if configPath == "" {
		log.Fatal("--config is required")
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	db, err := storage.OpenSQLite(ctx, storage.OpenOptions{
		Path:          cfg.DatabasePath,
		CRSQLitePath:  cfg.Extensions.CRSQLitePath,
		SQLiteVecPath: cfg.Extensions.SQLiteVecPath,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if _, err := storage.RunMigrations(ctx, db); err != nil {
		log.Fatal(err)
	}
	worker := indexer.NewWorker(db, 500*time.Millisecond)
	if diag {
		report, err := worker.Diagnostics(ctx)
		if err != nil {
			log.Fatal(err)
		}
		if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
			log.Fatal(err)
		}
		return
	}
	if once {
		if err := worker.ProcessOnce(ctx); err != nil {
			log.Fatal(err)
		}
		logIndexDiagnostics(ctx, worker)
		return
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := worker.ProcessOnce(ctx); err != nil {
			log.Printf("index cycle errors: %v", err)
		}
		logIndexDiagnostics(ctx, worker)
		select {
		case <-ctx.Done():
			log.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
}

func logIndexDiagnostics(ctx context.Context, worker *indexer.Worker) {
	report, err := worker.Diagnostics(ctx)
	if err != nil {
		log.Printf("index diagnostics unavailable: %v", err)
		return
	}
	raw, err := json.Marshal(report)
	if err != nil {
		log.Printf("index diagnostics marshal failed: %v", err)
		return
	}
	log.Printf("index_diag %s", raw)
}
