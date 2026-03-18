package main

import (
	"context"
	"flag"
	"log"
	"time"

	"crdt-agent-memory/internal/config"
	"crdt-agent-memory/internal/indexer"
	"crdt-agent-memory/internal/storage"
)

func main() {
	var configPath string
	var once bool
	flag.StringVar(&configPath, "config", "", "path to config yaml")
	flag.BoolVar(&once, "once", false, "process queued items once")
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
	if once {
		if err := worker.ProcessOnce(ctx); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := worker.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
