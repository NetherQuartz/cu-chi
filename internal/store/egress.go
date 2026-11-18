package store

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
)

// Changing egress preserves endpoint, all AWG keys and profile revisions.
func (s *Store) QueueEgress(ctx context.Context, u User, id, mode string) (string, error) {
	if mode != "direct" && mode != "warp" {
		return "", ErrConflict
	}
	tx, err := vpnTx(ctx, s)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var ready, busy bool
	var failure string
	var desired, applied string
	err = tx.QueryRow(ctx, `SELECT vpn_state='ready',egress_mode,applied_egress_mode,vpn_error,EXISTS(SELECT 1 FROM jobs WHERE server_id=servers.id AND status IN ('queued','running')) FROM servers WHERE id=$1 FOR UPDATE`, id).Scan(&ready, &desired, &applied, &failure, &busy)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if !ready {
		return "", ErrConflict
	}
	if mode == desired && (busy || mode == applied && failure == "") {
		return "", ErrConflict
	}
	// A second explicit selection supersedes a queued change. The single worker
	// and generation fencing serialize in-flight changes, including turning off.
	if _, err = tx.Exec(ctx, `UPDATE servers SET egress_mode=$2,desired_generation=desired_generation+1,vpn_error='' WHERE id=$1`, id, mode); err != nil {
		return "", err
	}
	job, err := enqueue(ctx, tx, id, u.ID, "sync")
	if err != nil {
		return "", err
	}
	if err = audit(ctx, tx, u.ID, "server.egress_"+mode+"_queued", id); err != nil {
		return "", err
	}
	return job, tx.Commit(ctx)
}

// First registration is immutable and encrypted under a server-specific namespace.
func (s *Store) SaveWarpConfig(ctx context.Context, id string, encrypted []byte) ([]byte, error) {
	var saved []byte
	err := s.Pool.QueryRow(ctx, `UPDATE servers SET encrypted_warp_config=COALESCE(encrypted_warp_config,$2) WHERE id=$1 RETURNING encrypted_warp_config`, id, encrypted).Scan(&saved)
	return saved, err
}
