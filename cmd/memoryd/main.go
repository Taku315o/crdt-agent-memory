package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"crdt-agent-memory/internal/api"
	"crdt-agent-memory/internal/config"
	"crdt-agent-memory/internal/embedding"
	"crdt-agent-memory/internal/indexer"
	"crdt-agent-memory/internal/memsync"
	"crdt-agent-memory/internal/policy"
	"crdt-agent-memory/internal/scrubber"
	"crdt-agent-memory/internal/signing"
	"crdt-agent-memory/internal/storage"
)

func main() {
	var configPath string
	var command string
	var withIndexer bool
	flag.StringVar(&configPath, "config", "", "path to config yaml")
	flag.StringVar(&command, "cmd", "serve", "command: migrate|diag|serve|keygen")
	flag.BoolVar(&withIndexer, "with-indexer", false, "run the index worker in-process")
	flag.Parse()

	// keygen command doesn't need a config
	if command == "keygen" {
		if configPath == "" {
			log.Fatal("--config is required for keygen")
		}
		cfg, err := config.Load(configPath)
		if err != nil {
			log.Fatal(err)
		}
		seed := seedForPeer(cfg.PeerID)
		signer, err := signing.NewSignerFromSeed(seed)
		if err != nil {
			log.Fatal(err)
		}
		// Write seed to file
		seedHex := hex.EncodeToString(seed)
		if err := os.WriteFile(cfg.SigningKeyPath, []byte(seedHex), 0o600); err != nil {
			log.Fatal(err)
		}
		// Output public key
		fmt.Println(signer.PublicKeyHex())
		return
	}

	if configPath == "" {
		log.Fatal("--config is required")
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatal(err)
	}
	embedding.Configure(cfg.Embedding)
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
		meta, err := storage.RunMigrationsWithOptions(ctx, db, storage.MigrationOptions{
			SearchProfile:  cfg.Search.Profile,
			RankingProfile: cfg.Search.RankingProfile,
			FTSTokenizer: cfg.Search.FTSTokenizer,
			EmbeddingDim: cfg.Embedding.Dimension,
		})
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
		meta, err := storage.RunMigrationsWithOptions(ctx, db, storage.MigrationOptions{
			SearchProfile:  cfg.Search.Profile,
			RankingProfile: cfg.Search.RankingProfile,
			FTSTokenizer: cfg.Search.FTSTokenizer,
			EmbeddingDim: cfg.Embedding.Dimension,
		})
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
		if withIndexer {
			go runIndexWorker(ctx, indexer.NewWorker(db, 500*time.Millisecond))
		}
		go runScrubberWorker(ctx, server.Scrubber)
		httpServer := &http.Server{
			Addr:              cfg.API.ListenAddr,
			Handler:           server.Handler(),
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       120 * time.Second,
		}
		log.Printf("memoryd listening on %s", cfg.API.ListenAddr)
		if err := httpServer.ListenAndServe(); err != nil {
			log.Fatal(err)
		}
	default:
		log.Fatalf("unsupported cmd: %s", command)
	}
}

func runIndexWorker(ctx context.Context, worker *indexer.Worker) {
	if worker == nil {
		return
	}
	if err := worker.Run(ctx); err != nil && err != context.Canceled {
		log.Printf("index worker stopped: %v", err)
	}
}

func runScrubberWorker(ctx context.Context, svc *scrubber.Service) {
	if svc == nil {
		return
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		report, err := svc.RunActiveRepair(ctx, 7*24*time.Hour)
		if err != nil {
			log.Printf("scrubber repair failed: %v", err)
		} else if report.DeletedQuarantineBatches > 0 || report.SuspendedEdges > 0 || report.SuspendedSignals > 0 || report.ResolvedEdgeSuspensions > 0 || report.ResolvedSignalSuspensions > 0 {
			log.Printf(
				"scrubber repair deleted_quarantine=%d suspended_edges=%d suspended_signals=%d resolved_edges=%d resolved_signals=%d",
				report.DeletedQuarantineBatches,
				report.SuspendedEdges,
				report.SuspendedSignals,
				report.ResolvedEdgeSuspensions,
				report.ResolvedSignalSuspensions,
			)
		}
		<-ticker.C
	}
}

func seedForPeer(peerID string) []byte {
	sum := sha256.Sum256([]byte("crdt-agent-memory/" + peerID))
	return sum[:]
}
