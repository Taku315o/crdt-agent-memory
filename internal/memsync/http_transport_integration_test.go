package memsync

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crdt-agent-memory/internal/memory"
	"crdt-agent-memory/internal/policy"
	"crdt-agent-memory/internal/storage"
	"crdt-agent-memory/internal/testenv"
)

type syncFixture struct {
	peerID string
	db     *sql.DB
	mem    *memory.Service
	sync   *Service
	server *httptest.Server
	meta   storage.Metadata
}

func newSyncFixture(t *testing.T, peerID string, allowPeers []string, allowedNamespaces map[string][]string, serve bool) *syncFixture {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), peerID+".sqlite")
	db, err := storage.OpenSQLite(ctx, storage.OpenOptions{
		Path:         dbPath,
		CRSQLitePath: testenv.CRSQLitePath(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	meta, err := storage.RunMigrations(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	requireSharedSyncTriggers(t, ctx, db)

	policies := policy.NewRepository(db)
	for _, allowPeer := range allowPeers {
		if err := policies.AllowPeer(ctx, allowPeer, allowPeer); err != nil {
			t.Fatal(err)
		}
	}

	fixture := &syncFixture{
		peerID: peerID,
		db:     db,
		mem:    memory.NewService(db),
		sync:   NewService(db, meta, policies, peerID, TransportHTTPDev),
		meta:   meta,
	}
	if serve {
		server := httptest.NewServer(NewHTTPServer(fixture.sync, func(peerID string) map[string]struct{} {
			return AllowedNamespaceSet(allowedNamespaces[peerID])
		}).Handler())
		fixture.server = server
		t.Cleanup(server.Close)
	}
	return fixture
}

func requireSharedSyncTriggers(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	for _, trigger := range []string{
		"trg_memory_nodes_sync_insert",
		"trg_memory_nodes_sync_update",
		"trg_memory_edges_sync_insert",
		"trg_memory_edges_sync_update",
		"trg_memory_signals_sync_insert",
		"trg_memory_signals_sync_update",
		"trg_artifact_refs_sync_insert",
		"trg_artifact_refs_sync_update",
	} {
		var count int
		if err := db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name = ?
		`, trigger).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("trigger %s missing", trigger)
		}
	}
}

func seedSharedAndPrivate(t *testing.T, fixture *syncFixture) {
	t.Helper()
	ctx := context.Background()
	if _, err := fixture.mem.Store(ctx, memory.StoreRequest{
		Visibility:    memory.VisibilityShared,
		Namespace:     "team/dev",
		Body:          "shared sync body",
		Subject:       "shared",
		OriginPeerID:  fixture.peerID,
		AuthorAgentID: "agent-a",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.mem.Store(ctx, memory.StoreRequest{
		Visibility:    memory.VisibilityPrivate,
		Namespace:     "local/dev",
		Body:          "private sync body",
		Subject:       "private",
		OriginPeerID:  fixture.peerID,
		AuthorAgentID: "agent-a",
	}); err != nil {
		t.Fatal(err)
	}
}

func syncCycleViaHTTP(t *testing.T, source, remote *syncFixture, namespaces []string, limit int) {
	t.Helper()
	ctx := context.Background()
	client := NewHTTPClient(remote.server.URL, 5*time.Second)
	if _, err := client.Handshake(ctx, HandshakeRequest{
		ProtocolVersion:              source.meta.ProtocolVersion,
		MinCompatibleProtocolVersion: source.meta.MinCompatibleProtocolVersion,
		PeerID:                       source.peerID,
		SchemaHash:                   source.meta.SchemaHash,
		CRRManifestHash:              source.meta.CRRManifestHash,
		Namespaces:                   namespaces,
	}); err != nil {
		t.Fatal(err)
	}
	for _, namespace := range namespaces {
		batch, err := source.sync.ExtractBatch(ctx, remote.peerID, namespace, limit)
		if err != nil {
			t.Fatal(err)
		}
		if err := client.Apply(ctx, ApplyRequest{FromPeerID: source.peerID, Batch: batch}); err != nil {
			t.Fatal(err)
		}
		remoteBatch, err := client.Pull(ctx, PullRequest{
			PeerID:    source.peerID,
			Namespace: namespace,
			Limit:     limit,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := source.sync.ApplyBatch(ctx, remote.peerID, remoteBatch); err != nil {
			t.Fatal(err)
		}
	}
}

func countRows(t *testing.T, ctx context.Context, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func cursorVersion(t *testing.T, ctx context.Context, db *sql.DB, peerID, namespace string) int64 {
	t.Helper()
	var version int64
	if err := db.QueryRowContext(ctx, `
		SELECT version FROM sync_cursors WHERE peer_id = ? AND namespace = ?
	`, peerID, namespace).Scan(&version); err != nil {
		t.Fatal(err)
	}
	return version
}

func TestHTTPTransportSyncSharedAndPrivate(t *testing.T) {
	source := newSyncFixture(t, "peer-a", nil, nil, false)
	remote := newSyncFixture(t, "peer-b", []string{"peer-a"}, map[string][]string{
		"peer-a": []string{"team/dev"},
	}, true)
	seedSharedAndPrivate(t, source)

	syncCycleViaHTTP(t, source, remote, []string{"team/dev"}, 100)

	ctx := context.Background()
	if got := countRows(t, ctx, remote.db, `SELECT COUNT(*) FROM memory_nodes`); got != 1 {
		t.Fatalf("memory_nodes count = %d, want 1", got)
	}
	if got := countRows(t, ctx, remote.db, `SELECT COUNT(*) FROM private_memory_nodes`); got != 0 {
		t.Fatalf("private_memory_nodes count = %d, want 0", got)
	}
}

func TestHTTPTransportReplaySafe(t *testing.T) {
	source := newSyncFixture(t, "peer-a", nil, nil, false)
	remote := newSyncFixture(t, "peer-b", []string{"peer-a"}, map[string][]string{
		"peer-a": []string{"team/dev"},
	}, true)
	seedSharedAndPrivate(t, source)

	ctx := context.Background()
	batch, err := source.sync.ExtractBatch(ctx, remote.peerID, "team/dev", 100)
	if err != nil {
		t.Fatal(err)
	}
	syncCycleViaHTTP(t, source, remote, []string{"team/dev"}, 100)
	syncCycleViaHTTP(t, source, remote, []string{"team/dev"}, 100)

	if got := countRows(t, ctx, remote.db, `SELECT COUNT(*) FROM memory_nodes`); got != 1 {
		t.Fatalf("memory_nodes count = %d, want 1", got)
	}
	if got := cursorVersion(t, ctx, remote.db, source.peerID, "team/dev"); got != batch.MaxVersion {
		t.Fatalf("cursor version = %d, want %d", got, batch.MaxVersion)
	}
}

func TestHTTPTransportSchemaMismatchFences(t *testing.T) {
	remote := newSyncFixture(t, "peer-b", []string{"peer-a"}, map[string][]string{
		"peer-a": []string{"team/dev"},
	}, true)
	client := NewHTTPClient(remote.server.URL, 5*time.Second)

	_, err := client.Handshake(context.Background(), HandshakeRequest{
		ProtocolVersion:              remote.meta.ProtocolVersion,
		MinCompatibleProtocolVersion: remote.meta.MinCompatibleProtocolVersion,
		PeerID:                       "peer-a",
		SchemaHash:                   "bad-schema",
		CRRManifestHash:              remote.meta.CRRManifestHash,
		Namespaces:                   []string{"team/dev"},
	})
	if err == nil {
		t.Fatal("expected schema mismatch error")
	}
	if !strings.Contains(err.Error(), "schema hash mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}

	status, err := remote.sync.SyncStatus(context.Background(), "team/dev")
	if err != nil {
		t.Fatal(err)
	}
	if !status.SchemaFenced {
		t.Fatal("expected schema_fenced=true")
	}
	if status.State != "schema_fenced" {
		t.Fatalf("state = %q, want schema_fenced", status.State)
	}
}

func TestHTTPTransportAllowlistRejectsPeer(t *testing.T) {
	remote := newSyncFixture(t, "peer-b", []string{"peer-a"}, map[string][]string{
		"peer-a": []string{"team/dev"},
	}, true)
	client := NewHTTPClient(remote.server.URL, 5*time.Second)

	_, err := client.Handshake(context.Background(), HandshakeRequest{
		ProtocolVersion:              remote.meta.ProtocolVersion,
		MinCompatibleProtocolVersion: remote.meta.MinCompatibleProtocolVersion,
		PeerID:                       "peer-z",
		SchemaHash:                   remote.meta.SchemaHash,
		CRRManifestHash:              remote.meta.CRRManifestHash,
		Namespaces:                   []string{"team/dev"},
	})
	if err == nil {
		t.Fatal("expected allowlist rejection")
	}
	if !strings.Contains(err.Error(), "namespace not allowlisted") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHTTPTransportNamespaceMismatchRejectsPull(t *testing.T) {
	remote := newSyncFixture(t, "peer-b", []string{"peer-a"}, map[string][]string{
		"peer-a": []string{"team/dev"},
	}, true)
	client := NewHTTPClient(remote.server.URL, 5*time.Second)

	if _, err := client.Handshake(context.Background(), HandshakeRequest{
		ProtocolVersion:              remote.meta.ProtocolVersion,
		MinCompatibleProtocolVersion: remote.meta.MinCompatibleProtocolVersion,
		PeerID:                       "peer-a",
		SchemaHash:                   remote.meta.SchemaHash,
		CRRManifestHash:              remote.meta.CRRManifestHash,
		Namespaces:                   []string{"team/dev"},
	}); err != nil {
		t.Fatal(err)
	}
	_, pullErr := client.Pull(context.Background(), PullRequest{
		PeerID:    "peer-a",
		Namespace: "local/dev",
		Limit:     10,
	})
	if pullErr == nil {
		t.Fatal("expected namespace mismatch rejection")
	}
	if !strings.Contains(pullErr.Error(), "namespace not allowlisted") {
		t.Fatalf("unexpected error: %v", pullErr)
	}
}
