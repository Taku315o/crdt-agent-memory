package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"strings"
	"time"

	"crdt-agent-memory/internal/config"
	"crdt-agent-memory/internal/memsync"
	"crdt-agent-memory/internal/policy"
	"crdt-agent-memory/internal/storage"
)

func main() {
	var configPath string
	var once bool
	flag.StringVar(&configPath, "config", "", "local config yaml")
	flag.BoolVar(&once, "once", false, "run one sync cycle and exit")
	flag.Parse()

	if configPath == "" {
		log.Fatal("--config is required")
	}
	ctx := context.Background()
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatal(err)
	}
	db, err := storage.OpenSQLite(ctx, storage.OpenOptions{
		Path:          cfg.DatabasePath,
		CRSQLitePath:  cfg.Extensions.CRSQLitePath,
		SQLiteVecPath: cfg.Extensions.SQLiteVecPath,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	meta, err := storage.RunMigrations(ctx, db)
	if err != nil {
		log.Fatal(err)
	}
	policies := policy.NewRepository(db)
	if err := policies.SyncRegistry(ctx, cfg.PeerRegistry); err != nil {
		log.Fatal(err)
	}
	svc := memsync.NewService(db, meta, policies, cfg.PeerID, "http-dev")
	allowedByPeer := make(map[string]map[string]struct{}, len(cfg.PeerRegistry))
	for _, peer := range cfg.PeerRegistry {
		allowedByPeer[peer.PeerID] = memsync.AllowedNamespaceSet(peer.NamespaceAllowlist)
	}

	server := &http.Server{
		Addr: cfg.Sync.ListenAddr,
		Handler: (&memsync.HTTPServer{
			Service: svc,
			AllowedNamespaces: func(peerID string) map[string]struct{} {
				return allowedByPeer[peerID]
			},
		}).Handler(),
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("sync server: %v", err)
		}
	}()
	log.Printf("syncd listening on %s", cfg.Sync.ListenAddr)

	runOnce := func() error {
		for _, peer := range cfg.PeerRegistry {
			namespaces := memsync.IntersectNamespaces(cfg.Namespaces, peer.NamespaceAllowlist)
			if len(namespaces) == 0 {
				continue
			}
			client := &memsync.HTTPClient{
				BaseURL: strings.TrimRight(peer.SyncURL, "/"),
				Client:  &http.Client{Timeout: time.Duration(cfg.Sync.OnceTimeout) * time.Millisecond},
			}
			if _, err := client.Handshake(ctx, memsync.HandshakeRequest{
				ProtocolVersion:              meta.ProtocolVersion,
				MinCompatibleProtocolVersion: meta.MinCompatibleProtocolVersion,
				PeerID:                       cfg.PeerID,
				SchemaHash:                   meta.SchemaHash,
				CRRManifestHash:              meta.CRRManifestHash,
				Namespaces:                   namespaces,
			}); err != nil {
				return err
			}
			for _, namespace := range namespaces {
				batch, err := svc.ExtractBatch(ctx, peer.PeerID, namespace, cfg.Sync.BatchLimit)
				if err != nil {
					return err
				}
				if err := client.Apply(ctx, memsync.ApplyRequest{FromPeerID: cfg.PeerID, Batch: batch}); err != nil {
					return err
				}
				remoteBatch, err := client.Pull(ctx, memsync.PullRequest{
					PeerID:    cfg.PeerID,
					Namespace: namespace,
					Limit:     cfg.Sync.BatchLimit,
				})
				if err != nil {
					return err
				}
				if err := svc.ApplyBatch(ctx, peer.PeerID, remoteBatch); err != nil {
					return err
				}
				status, err := svc.SyncStatus(ctx, namespace)
				if err != nil {
					return err
				}
				raw, _ := json.Marshal(status)
				log.Printf("sync_status %s", raw)
			}
		}
		return nil
	}

	if once {
		if err := runOnce(); err != nil {
			log.Fatal(err)
		}
		_ = server.Shutdown(ctx)
		return
	}

	ticker := time.NewTicker(time.Duration(cfg.Sync.IntervalMS) * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := runOnce(); err != nil {
			log.Printf("sync cycle failed: %v", err)
		}
		<-ticker.C
	}
}
