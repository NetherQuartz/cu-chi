package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/NetherQuartz/cu-chi/internal/cryptobox"
	"github.com/jackc/pgx/v5"
)

type Gateway struct {
	AppliedServerName string `json:"applied_server_name,omitempty"`
	Server
	DeviceID            string     `json:"device_id"`
	RouterIP            string     `json:"router_ip"`
	ClientCIDRs         string     `json:"client_cidrs"`
	State               string     `json:"state"`
	Error               string     `json:"error"`
	Kind                string     `json:"kind"`
	Attempts            int        `json:"attempts"`
	DesiredProfileID    string     `json:"desired_profile_id"`
	AppliedProfileID    string     `json:"applied_profile_id"`
	AppliedAt           *time.Time `json:"applied_at"`
	CurrentProfileID    string     `json:"current_profile_id"`
	Generation          int64      `json:"-"`
	RunToken            string     `json:"-"`
	EncryptedCredential []byte     `json:"-"`
	EncryptedLAN        []byte     `json:"-"`
}

const gatewaySelect = `SELECT g.id::text,g.device_id::text,g.name,g.hostname,g.router_ip,g.client_cidrs,g.ssh_port,g.ssh_user,g.host_key_fingerprint,g.host_key_policy,g.ssh_auth_method,COALESCE(g.ssh_key_id::text,''),g.created_at,g.state,g.error,g.kind,g.attempts,COALESCE(g.desired_profile_id::text,''),COALESCE(g.applied_profile_id::text,''),g.applied_at,COALESCE((SELECT p.id::text FROM profiles p WHERE p.device_id=g.device_id AND p.desired AND p.state='ready'),''),g.generation,COALESCE(g.run_token::text,''),CASE WHEN g.ssh_auth_method='saved_key' THEN k.encrypted_private_key ELSE g.encrypted_credential END,g.encrypted_lan,COALESCE((SELECT s.display_name FROM profiles p JOIN servers s ON s.id=p.server_id WHERE p.id=g.applied_profile_id),''),COALESCE((SELECT s.signature_revision FROM profiles p JOIN servers s ON s.id=p.server_id WHERE p.id=g.desired_profile_id),0) FROM gateways g LEFT JOIN ssh_keys k ON k.id=g.ssh_key_id `

func scanGateway(row pgx.Row) (g Gateway, err error) {
	err = row.Scan(&g.ID, &g.DeviceID, &g.Name, &g.Hostname, &g.RouterIP, &g.ClientCIDRs, &g.SSHPort, &g.SSHUser, &g.Fingerprint, &g.HostKeyPolicy, &g.AuthMethod, &g.SSHKeyID, &g.CreatedAt, &g.State, &g.Error, &g.Kind, &g.Attempts, &g.DesiredProfileID, &g.AppliedProfileID, &g.AppliedAt, &g.CurrentProfileID, &g.Generation, &g.RunToken, &g.EncryptedCredential, &g.EncryptedLAN, &g.AppliedServerName, &g.SignatureRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return
}
func (s *Store) Gateways(ctx context.Context) ([]Gateway, error) {
	rows, err := s.Pool.Query(ctx, gatewaySelect+`ORDER BY g.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Gateway{}
	for rows.Next() {
		g, err := scanGateway(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
func (s *Store) Gateway(ctx context.Context, id string) (Gateway, error) {
	return scanGateway(s.Pool.QueryRow(ctx, gatewaySelect+`WHERE g.id=$1`, id))
}
func (s *Store) CreateGateway(ctx context.Context, u User, g Gateway) (Gateway, error) {
	tx, err := vpnTx(ctx, s)
	if err != nil {
		return g, err
	}
	defer tx.Rollback(ctx)
	g.DeviceID = cryptobox.ID()
	_, err = tx.Exec(ctx, `INSERT INTO devices(id,user_id,name,platform) VALUES($1,$2,$3,'gateway')`, g.DeviceID, u.ID, g.Name)
	if err != nil {
		return g, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO gateways(id,device_id,name,hostname,router_ip,client_cidrs,ssh_port,ssh_user,host_key_fingerprint,host_key_policy,ssh_auth_method,ssh_key_id,encrypted_credential,encrypted_lan,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NULLIF($12,'')::uuid,$13,$14,$15) RETURNING created_at,state`, g.ID, g.DeviceID, g.Name, g.Hostname, g.RouterIP, g.ClientCIDRs, g.SSHPort, g.SSHUser, g.Fingerprint, g.HostKeyPolicy, g.AuthMethod, g.SSHKeyID, g.EncryptedCredential, g.EncryptedLAN, u.ID).Scan(&g.CreatedAt, &g.State)
	if err != nil {
		return g, err
	}
	if err = audit(ctx, tx, u.ID, "gateway.registered", g.ID); err != nil {
		return g, err
	}
	return g, tx.Commit(ctx)
}
func (s *Store) QueueGateway(ctx context.Context, u User, id, kind string) error {
	if kind != "check" && kind != "deploy" {
		return ErrConflict
	}
	tx, err := vpnTx(ctx, s)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var state, device string
	err = tx.QueryRow(ctx, `SELECT state,device_id::text FROM gateways WHERE id=$1 FOR UPDATE`, id).Scan(&state, &device)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if state == "queued" || state == "running" {
		return ErrConflict
	}
	profile := ""
	if kind == "deploy" {
		err = tx.QueryRow(ctx, `SELECT id::text FROM profiles WHERE device_id=$1 AND desired AND state='ready'`, device).Scan(&profile)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE gateways SET kind=$2,state='queued',generation=generation+1,attempts=0,error='',run_after=now(),desired_profile_id=NULLIF($3,'')::uuid WHERE id=$1`, id, kind, profile)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE notifications SET expires_at=LEAST(expires_at,now()) WHERE gateway_id=$1`, id); err != nil {
		return err
	}
	if err = audit(ctx, tx, u.ID, "gateway."+kind+"_queued", id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) ClaimGateway(ctx context.Context) (Gateway, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Gateway{}, err
	}
	defer tx.Rollback(ctx)
	var id string
	err = tx.QueryRow(ctx, `UPDATE gateways SET state='running',attempts=attempts+1,run_token=gen_random_uuid() WHERE id=(SELECT id FROM gateways WHERE state='queued' AND run_after<=now() ORDER BY run_after FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING id::text`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Gateway{}, ErrNotFound
	}
	if err != nil {
		return Gateway{}, err
	}
	g, err := scanGateway(tx.QueryRow(ctx, gatewaySelect+`WHERE g.id=$1`, id))
	if err != nil {
		return g, err
	}
	return g, tx.Commit(ctx)
}
func (s *Store) FinishGateway(ctx context.Context, g Gateway, failure string) error {
	tx, err := vpnTx(ctx, s)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if failure == "" && g.Kind == "deploy" {
		var current bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM profiles WHERE id=$1 AND device_id=$2 AND desired AND state='ready')`, g.DesiredProfileID, g.DeviceID).Scan(&current); err != nil {
			return err
		}
		if !current {
			failure = "Profile changed during deployment; apply the current profile again"
		}
	}
	state := "checked"
	if g.Kind == "deploy" {
		state = "ready"
	}
	if failure != "" {
		state = "failed"
		if g.Attempts < 3 {
			state = "queued"
		}
	}
	result, err := tx.Exec(ctx, `UPDATE gateways SET state=$4,error=$5,run_after=now()+interval '30 seconds',applied_profile_id=CASE WHEN $4='ready' THEN desired_profile_id ELSE applied_profile_id END,applied_at=CASE WHEN $4='ready' THEN now() ELSE applied_at END WHERE id=$1 AND generation=$2 AND run_token=$3 AND state='running'`, g.ID, g.Generation, g.RunToken, state, failure)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 1 && state == "ready" {
		if _, err = tx.Exec(ctx, `UPDATE profiles SET signature_revision=GREATEST(signature_revision,$2) WHERE id=$1`, g.DesiredProfileID, g.SignatureRevision); err != nil {
			return err
		}
	}
	if result.RowsAffected() == 1 && state == "failed" {
		if _, err = tx.Exec(ctx, `INSERT INTO notifications(user_id,event_key,kind,severity,gateway_id) SELECT id,$1,'job_failed','warning',$2 FROM users WHERE role='admin' ON CONFLICT DO NOTHING`, fmt.Sprintf("gateway:%s:%d", g.ID, g.Generation), g.ID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
