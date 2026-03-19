package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"crdt-agent-memory/internal/api"
	"crdt-agent-memory/internal/config"
	"crdt-agent-memory/internal/memsync"
	"crdt-agent-memory/internal/policy"
	"crdt-agent-memory/internal/scrubber"
	"crdt-agent-memory/internal/signing"
	"crdt-agent-memory/internal/storage"
)

func main() {
	var configPath string
	var command string
	flag.StringVar(&configPath, "config", "", "path to config yaml")
	flag.StringVar(&command, "cmd", "serve", "command: migrate|diag|serve")
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
		signer, err := signing.LoadSigner(cfg.SigningKeyPath)
		if err != nil {
			log.Fatal(err)
		}
		summary, err := scrubber.NewService(db, cfg.PeerID, signer.PublicKeyHex()).Diagnose(ctx)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Fprintf(
			os.Stdout,
			"schema_hash=%s\ncrr_manifest_hash=%s\nprotocol=%s\nvalid_signatures=%d\nmissing_signatures=%d\ninvalid_signatures=%d\nunknown_peer_rows=%d\norphan_edges=%d\norphan_signals=%d\n",
			meta.SchemaHash,
			meta.CRRManifestHash,
			meta.ProtocolVersion,
			summary.TrustSummary.ValidSignatures,
			summary.TrustSummary.MissingSignatures,
			summary.TrustSummary.InvalidSignatures,
			summary.TrustSummary.UnknownPeerRows,
			summary.ScrubberSummary.OrphanEdges,
			summary.ScrubberSummary.OrphanSignals,
		)
	case "serve":
		meta, err := storage.RunMigrations(ctx, db)
		if err != nil {
			log.Fatal(err)
		}
		signer, err := signing.LoadSigner(cfg.SigningKeyPath)
		if err != nil {
			log.Fatal(err)
		}
		policies := policy.NewRepository(db)
		if err := policies.SyncRegistry(ctx, cfg.PeerRegistry); err != nil {
			log.Fatal(err)
		}
		syncSvc := memsync.NewService(db, meta, policies, cfg.PeerID, memsync.TransportHTTPDev)
		server, err := api.New(ctx, db, meta, syncSvc, signer, cfg.PeerID)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("memoryd listening on %s", cfg.API.ListenAddr)
		if err := http.ListenAndServe(cfg.API.ListenAddr, server.Handler()); err != nil {
			log.Fatal(err)
		}
	default:
		log.Fatalf("unsupported cmd: %s", command)
	}
}
