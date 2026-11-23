package store

import (
	"context"
	"errors"

	"github.com/NetherQuartz/cu-chi/internal/awg"
	"github.com/jackc/pgx/v5"
)

func signatureMode(value string) string {
	switch value {
	case "":
		return "off"
	case awg.DefaultSignature():
		return "amnezia"
	default:
		return "custom"
	}
}

// Change the client export without replacing server keys or queuing peer changes.
func (s *Store) SetSignature(ctx context.Context, u User, id, signature string, expected int64) error {
	tx, err := vpnTx(ctx, s)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var current string
	var revision int64
	err = tx.QueryRow(ctx, "SELECT awg_signature,signature_revision FROM servers WHERE id=$1 AND deleted_at IS NULL FOR UPDATE", id).Scan(&current, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if current == signature {
		return nil
	}
	if revision != expected {
		return ErrServerChanged
	}
	if _, err = tx.Exec(ctx, "UPDATE servers SET awg_signature=$2,signature_revision=signature_revision+1 WHERE id=$1", id, signature); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE notifications SET expires_at=LEAST(expires_at,now()) WHERE server_id=$1 AND kind='awg_profile_update'", id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO notifications(user_id,event_key,kind,severity,server_id)
 SELECT DISTINCT u.id,'awg:signature:'||s.id::text||':'||s.signature_revision::text,'awg_profile_update','warning',s.id
 FROM servers s JOIN profiles p ON p.server_id=s.id JOIN devices d ON d.id=p.device_id JOIN users u ON u.id=d.user_id
 WHERE s.id=$1 AND p.desired AND p.signature_revision<s.signature_revision AND u.role<>'denied'`, id); err != nil {
		return err
	}
	if err = audit(ctx, tx, u.ID, "server.signature_changed", id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ConfirmProfileImport(ctx context.Context, u User, id string, revision int64) error {
	tx, err := vpnTx(ctx, s)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var current int64
	err = tx.QueryRow(ctx, `SELECT s.signature_revision FROM profiles p JOIN devices d ON d.id=p.device_id JOIN servers s ON s.id=p.server_id
 WHERE p.id=$1 AND (d.user_id=$2 OR $3) AND d.platform<>'gateway' AND p.desired AND p.state='ready' AND s.deleted_at IS NULL`, id, u.ID, u.Role == "admin").Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if revision != current {
		return ErrServerChanged
	}
	if _, err = tx.Exec(ctx, "UPDATE profiles SET signature_revision=$2 WHERE id=$1", id, revision); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
