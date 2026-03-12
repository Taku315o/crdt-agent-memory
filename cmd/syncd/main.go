package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"crdt-agent-memory/internal/config"
	"crdt-agent-memory/internal/memsync"
	"crdt-agent-memory/internal/policy"
	"crdt-agent-memory/internal/storage"
)

func main() {
	var configPath string
	var peerConfigPath string
	var namespace string
	flag.StringVar(&configPath, "config", "", "local config yaml")
	flag.StringVar(&peerConfigPath, "peer-config", "", "remote peer config yaml")
	flag.StringVar(&namespace, "namespace", "", "namespace to sync")
	flag.Parse()

	if configPath == "" || peerConfigPath == "" || namespace == "" {
		log.Fatal("--config, --peer-config, and --namespace are required")
	}

	ctx := context.Background()
	localCfg, err := config.Load(configPath)
	if err != nil {
		log.Fatal(err)
	}
	remoteCfg, err := config.Load(peerConfigPath)
	if err != nil {
		log.Fatal(err)
	}

	localDB, err := storage.OpenSQLite(ctx, localCfg.DatabasePath)
	if err != nil {
		log.Fatal(err)
	}
	defer localDB.Close()
	remoteDB, err := storage.OpenSQLite(ctx, remoteCfg.DatabasePath)
	if err != nil {
		log.Fatal(err)
	}
	defer remoteDB.Close()

	localMeta, err := storage.LoadMetadata(ctx, localDB)
	if err != nil {
		log.Fatal(err)
	}
	remoteMeta, err := storage.LoadMetadata(ctx, remoteDB)
	if err != nil {
		log.Fatal(err)
	}

	localPolicies := policy.NewRepository(localDB)
	remotePolicies := policy.NewRepository(remoteDB)
	if err := localPolicies.AllowPeer(ctx, remoteCfg.PeerID, remoteCfg.PeerID); err != nil {
		log.Fatal(err)
	}
	if err := remotePolicies.AllowPeer(ctx, localCfg.PeerID, localCfg.PeerID); err != nil {
		log.Fatal(err)
	}

	left := memsync.NewService(localDB, localMeta, localPolicies, localCfg.PeerID)
	right := memsync.NewService(remoteDB, remoteMeta, remotePolicies, remoteCfg.PeerID)
	if err := memsync.SyncPair(ctx, left, right, namespace, 1000); err != nil {
		log.Fatal(err)
	}

	diag, err := left.Diagnostics(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("sync_ok schema_hash=%s tracked_peers=%d quarantine=%d\n", diag.SchemaHash, len(diag.TrackedPeers), diag.QuarantineCount)
}
