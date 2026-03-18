package policy

import (
	"context"
	"database/sql"
	"time"

	"crdt-agent-memory/internal/config"
)

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) AllowPeer(ctx context.Context, peerID, displayName string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO peer_policies(peer_id, display_name, trust_state, trust_weight, updated_at_ms)
		VALUES(?, ?, 'allow', 1.0, ?)
		ON CONFLICT(peer_id) DO UPDATE SET
			display_name = excluded.display_name,
			trust_state = 'allow',
			updated_at_ms = excluded.updated_at_ms
	`, peerID, displayName, time.Now().UnixMilli())
	return err
}

func (r *Repository) SyncRegistry(ctx context.Context, peers []config.PeerRegistryEntry) error {
	for _, peer := range peers {
		_, err := r.db.ExecContext(ctx, `
			INSERT INTO peer_policies(peer_id, display_name, trust_state, trust_weight, discovery_profile, relay_profile, updated_at_ms)
			VALUES(?, ?, 'allow', 1.0, ?, ?, ?)
			ON CONFLICT(peer_id) DO UPDATE SET
				display_name = excluded.display_name,
				trust_state = 'allow',
				discovery_profile = excluded.discovery_profile,
				relay_profile = excluded.relay_profile,
				updated_at_ms = excluded.updated_at_ms
		`, peer.PeerID, peer.DisplayName, peer.DiscoveryProfile, peer.RelayProfile, time.Now().UnixMilli())
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) IsAllowed(ctx context.Context, peerID string) (bool, error) {
	allowed := false
	err := r.db.QueryRowContext(ctx, `
		SELECT COALESCE(trust_state = 'allow', 0)
		FROM peer_policies
		WHERE peer_id = ?
	`, peerID).Scan(&allowed)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return allowed, err
}
