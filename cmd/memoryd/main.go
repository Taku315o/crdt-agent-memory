package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"crdt-agent-memory/internal/config"
	"crdt-agent-memory/internal/storage"
)

func main() {
	var configPath string
	var command string
	flag.StringVar(&configPath, "config", "", "path to config yaml")
	flag.StringVar(&command, "cmd", "diag", "command: migrate|diag")
	flag.Parse()

	if configPath == "" {
		log.Fatal("--config is required")
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	db, err := storage.OpenSQLite(ctx, cfg.DatabasePath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	switch command {
	case "migrate":
		meta, err := storage.RunMigrations(ctx, db)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Fprintf(os.Stdout, "schema_hash=%s\ncrr_manifest_hash=%s\n", meta.SchemaHash, meta.CRRManifestHash)
	case "diag":
		meta, err := storage.LoadMetadata(ctx, db)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Fprintf(os.Stdout, "schema_hash=%s\ncrr_manifest_hash=%s\nprotocol=%s\n", meta.SchemaHash, meta.CRRManifestHash, meta.ProtocolVersion)
	default:
		log.Fatalf("unsupported cmd: %s", command)
	}
}
