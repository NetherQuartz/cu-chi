package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// A server-specific lock protects remote operations without holding the VPN queue lock over SSH.
const serverLockKey = "hashtextextended($1,718352107)"

func (s *Store) BeginServerUse(ctx context.Context, id string) (pgx.Tx, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock_shared("+serverLockKey+")", id); err != nil {
		tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}

var ErrServerBusy = errors.New("server operation in progress")
var ErrServerChanged = errors.New("server settings changed")

func lockServerEdit(ctx context.Context, tx pgx.Tx, id string) error {
	var acquired bool
	if err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock("+serverLockKey+")", id).Scan(&acquired); err != nil {
		return err
	}
	if !acquired {
		return ErrServerBusy
	}
	// A gateway deployment also consumes this server's exported endpoint and keys.
	// Lock queued gateway rows against a concurrent claim until the edit commits.
	rows, err := tx.Query(ctx, `SELECT g.state FROM gateways g JOIN profiles p ON p.id=g.desired_profile_id
     WHERE p.server_id=$1 AND g.kind='deploy' AND g.state IN ('queued','running') FOR UPDATE OF g`, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		if err = rows.Scan(&state); err != nil {
			return err
		}
		if state == "running" {
			return ErrServerBusy
		}
	}
	return rows.Err()
}

func (s *Store) EditServer(ctx context.Context, u User, v Server, encrypted []byte) error {
	tx, err := vpnTx(ctx, s)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockServerEdit(ctx, tx, v.ID); err != nil {
		return err
	}
	var method, endpoint string
	var revision int64
	err = tx.QueryRow(ctx, "SELECT ssh_auth_method,endpoint,edit_revision FROM servers WHERE id=$1 AND deleted_at IS NULL FOR UPDATE", v.ID).Scan(&method, &endpoint, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if revision != v.EditRevision || (endpoint == "") != (v.Endpoint == "") {
		return ErrServerChanged
	}
	if encrypted == nil && v.AuthMethod != "saved_key" && method != v.AuthMethod {
		return ErrConflict
	}
	_, err = tx.Exec(ctx, `UPDATE servers SET name=$2,hostname=$3,ssh_port=$4,ssh_user=$5,ssh_auth_method=$6,
  ssh_key_id=NULLIF($7,'')::uuid,encrypted_private_key=CASE WHEN $6='saved_key' THEN NULL ELSE COALESCE($8,encrypted_private_key) END,
  country_code=$9,city=$10,endpoint=$11,edit_revision=edit_revision+1 WHERE id=$1`, v.ID, v.Name, v.Hostname, v.SSHPort, v.SSHUser, v.AuthMethod, v.SSHKeyID, encrypted, v.CountryCode, v.City, v.Endpoint)
	if err != nil {
		return err
	}
	if err = audit(ctx, tx, u.ID, "server.updated", v.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) DeleteServer(ctx context.Context, u User, id string) error {
	tx, err := vpnTx(ctx, s)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockServerEdit(ctx, tx, id); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE servers SET deleted_at=now(),vpn_state='deleted',ssh_key_id=NULL,encrypted_private_key=NULL,
 encrypted_vpn_settings=NULL,encrypted_warp_config=NULL,vpn_error='' WHERE id=$1 AND deleted_at IS NULL`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	for _, stmt := range []string{
		"UPDATE vpn_preferences SET default_server_id=NULL WHERE default_server_id=$1",
		"UPDATE jobs SET status='cancelled',stage='cancelled',run_token=NULL,finished_at=now() WHERE server_id=$1 AND status IN ('queued','running')",
		`UPDATE gateways g SET state=CASE WHEN applied_profile_id IS NULL THEN 'new' ELSE 'ready' END,
     generation=generation+1,run_token=NULL,desired_profile_id=NULL,error=''
   FROM profiles p WHERE p.id=g.desired_profile_id AND p.server_id=$1 AND g.kind='deploy' AND g.state='queued'`,
		"UPDATE profiles SET state='archived' WHERE server_id=$1 AND state<>'revoked'",
		`INSERT INTO archived_xray_devices(device_id,server_id)
    SELECT d.device_id,n.server_id FROM xray_devices d JOIN xray_nodes n ON n.server_id=$1
    WHERE d.desired AND n.applied>=d.revision`,
		"DELETE FROM xray_nodes WHERE server_id=$1",
		"UPDATE notifications SET expires_at=LEAST(expires_at,now()) WHERE server_id=$1",
	} {
		if _, err = tx.Exec(ctx, stmt, id); err != nil {
			return err
		}
	}
	if err = pruneRevokedXDevices(ctx, tx); err != nil {
		return err
	}
	if err = audit(ctx, tx, u.ID, "server.deleted", id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
