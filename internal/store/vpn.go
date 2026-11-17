package store

import (
	"context"
	"errors"
	"fmt"
	"github.com/NetherQuartz/cu-chi/internal/cryptobox"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrConflict = errors.New("operation conflicts with current VPN state")

type Profile struct {
	NeedsAttention bool      `json:"needs_attention"`
	ID             string    `json:"id"`
	DeviceID       string    `json:"device_id"`
	ServerID       string    `json:"server_id"`
	ServerName     string    `json:"server_name"`
	Revision       int       `json:"revision"`
	Address        string    `json:"address"`
	PublicKey      string    `json:"public_key"`
	State          string    `json:"state"`
	Desired        bool      `json:"desired"`
	CreatedAt      time.Time `json:"created_at"`
	EncryptedKeys  []byte    `json:"-"`
}
type Job struct {
	RunToken  string    `json:"-"`
	ID        string    `json:"id"`
	ServerID  string    `json:"server_id"`
	Kind      string    `json:"kind"`
	Status    string    `json:"status"`
	Stage     string    `json:"stage"`
	Attempts  int       `json:"attempts"`
	Error     string    `json:"error"`
	CreatedAt time.Time `json:"created_at"`
}
type Target struct {
	Server
	EncryptedKey, EncryptedSettings []byte
	Generation                      int64
	Profiles                        []Profile
}

// Mutations are short transactions. The worker never holds this lock over SSH.
func vpnTx(ctx context.Context, s *Store) (pgx.Tx, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(718352105)"); err != nil {
		tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}
func enqueue(ctx context.Context, tx pgx.Tx, serverID, actor, kind string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `INSERT INTO jobs(server_id,created_by,kind) VALUES($1,$2,$3)
 ON CONFLICT(server_id) WHERE status='queued' DO UPDATE SET kind=CASE WHEN jobs.kind='deploy' OR excluded.kind='deploy' THEN 'deploy' WHEN jobs.kind='sync' OR excluded.kind='sync' THEN 'sync' ELSE 'check' END, run_after=now(), attempts=0,error='' RETURNING id::text`, serverID, actor, kind).Scan(&id)
	return id, err
}
func (s *Store) QueueServer(ctx context.Context, u User, id, kind, endpoint string, port int, settings []byte) (string, error) {
	tx, err := vpnTx(ctx, s)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var oldEndpoint string
	var oldPort int
	var initialized bool
	err = tx.QueryRow(ctx, "SELECT endpoint,vpn_port,encrypted_vpn_settings IS NOT NULL FROM servers WHERE id=$1 FOR UPDATE", id).Scan(&oldEndpoint, &oldPort, &initialized)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if kind == "deploy" {
		if initialized && (oldEndpoint != endpoint || oldPort != port) {
			return "", ErrConflict
		}
		_, err = tx.Exec(ctx, `UPDATE servers SET endpoint=$2,vpn_port=$3,encrypted_vpn_settings=COALESCE(encrypted_vpn_settings,$4),desired_generation=desired_generation+1,vpn_state='deploying',vpn_error='' WHERE id=$1`, id, endpoint, port, settings)
		if err != nil {
			return "", err
		}
	}
	job, err := enqueue(ctx, tx, id, u.ID, kind)
	if err != nil {
		return "", err
	}
	if err = audit(ctx, tx, u.ID, "server."+kind+"_queued", id); err != nil {
		return "", err
	}
	return job, tx.Commit(ctx)
}
func (s *Store) SetDefaultServer(ctx context.Context, u User, id string) error {
	tx, err := vpnTx(ctx, s)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var ready bool
	err = tx.QueryRow(ctx, "SELECT vpn_state='ready' FROM servers WHERE id=$1", id).Scan(&ready)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !ready {
		return ErrConflict
	}
	var unchanged bool
	if err = tx.QueryRow(ctx, `SELECT COALESCE(default_server_id=$1,false) FROM vpn_preferences`, id).Scan(&unchanged); err != nil {
		return err
	}
	if unchanged {
		return nil
	}
	// A previous migration campaign no longer describes the current main-server change.
	if _, err = tx.Exec(ctx, `UPDATE notifications SET expires_at=LEAST(expires_at,now()) WHERE kind='reminder'`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE vpn_preferences SET default_server_id=$1", id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO notifications(user_id,event_key,kind,severity,server_id) SELECT DISTINCT u.id,$2,'reminder','warning',$1::uuid FROM users u JOIN devices d ON d.user_id=u.id JOIN profiles p ON p.device_id=d.id WHERE u.role<>'denied' AND p.desired AND p.server_id<>$1::uuid ON CONFLICT DO NOTHING`, id, "reminder:change:"+cryptobox.ID()); err != nil {
		return err
	}
	if err = audit(ctx, tx, u.ID, "server.default_changed", id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) Issue(ctx context.Context, u User, p Profile) (Profile, error) {
	tx, err := vpnTx(ctx, s)
	if err != nil {
		return p, err
	}
	defer tx.Rollback(ctx)
	var deviceID string
	err = tx.QueryRow(ctx, "SELECT id::text FROM devices WHERE id=$1 AND (user_id=$2 OR $3) FOR UPDATE", p.DeviceID, u.ID, u.Role == "admin").Scan(&deviceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	var next int
	err = tx.QueryRow(ctx, `SELECT s.id::text,s.next_address FROM servers s JOIN vpn_preferences v ON v.default_server_id=s.id WHERE s.vpn_state='ready' FOR UPDATE OF s`).Scan(&p.ServerID, &next)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrConflict
	}
	if err != nil {
		return p, err
	}
	if next >= 65534 {
		return p, ErrConflict
	}
	// Refuse repeated clicks while the same device has an unapplied revision.
	var pending bool
	err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM profiles WHERE device_id=$1 AND desired AND state='pending')", deviceID).Scan(&pending)
	if err != nil {
		return p, err
	}
	if pending {
		return p, ErrConflict
	}
	rows, err := tx.Query(ctx, "UPDATE profiles SET desired=false,state='revoking' WHERE device_id=$1 AND desired RETURNING server_id::text", deviceID)
	if err != nil {
		return p, err
	}
	changed := map[string]bool{p.ServerID: true}
	for rows.Next() {
		var server string
		if err = rows.Scan(&server); err != nil {
			rows.Close()
			return p, err
		}
		changed[server] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return p, err
	}
	p.Address = fmt.Sprintf("10.66.%d.%d", next/256, next%256)
	err = tx.QueryRow(ctx, "SELECT COALESCE(max(revision),0)+1 FROM profiles WHERE device_id=$1", deviceID).Scan(&p.Revision)
	if err != nil {
		return p, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO profiles(id,device_id,server_id,revision,address,public_key,encrypted_keys) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING created_at`, p.ID, deviceID, p.ServerID, p.Revision, p.Address, p.PublicKey, p.EncryptedKeys).Scan(&p.CreatedAt)
	if err != nil {
		return p, err
	}
	if _, err = tx.Exec(ctx, "UPDATE servers SET next_address=next_address+1 WHERE id=$1", p.ServerID); err != nil {
		return p, err
	}
	for id := range changed {
		if _, err = tx.Exec(ctx, "UPDATE servers SET desired_generation=desired_generation+1 WHERE id=$1", id); err != nil {
			return p, err
		}
		if _, err = enqueue(ctx, tx, id, u.ID, "sync"); err != nil {
			return p, err
		}
	}
	if err = audit(ctx, tx, u.ID, "profile.issued", p.ID); err != nil {
		return p, err
	}
	p.State = "pending"
	p.Desired = true
	return p, tx.Commit(ctx)
}
func (s *Store) Revoke(ctx context.Context, u User, deviceID string) error {
	tx, err := vpnTx(ctx, s)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var found string
	err = tx.QueryRow(ctx, "SELECT id::text FROM devices WHERE id=$1 AND (user_id=$2 OR $3) FOR UPDATE", deviceID, u.ID, u.Role == "admin").Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, "UPDATE profiles SET desired=false,state='revoking' WHERE device_id=$1 AND state<>'revoked' RETURNING server_id::text", deviceID)
	if err != nil {
		return err
	}
	ids := map[string]bool{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids[id] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for id := range ids {
		if _, err = tx.Exec(ctx, "UPDATE servers SET desired_generation=desired_generation+1 WHERE id=$1", id); err != nil {
			return err
		}
		if _, err = enqueue(ctx, tx, id, u.ID, "sync"); err != nil {
			return err
		}
	}
	if err = audit(ctx, tx, u.ID, "profile.revocation_requested", deviceID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) Profiles(ctx context.Context, u User) ([]Profile, error) {
	rows, err := s.Pool.Query(ctx, `SELECT p.id::text,p.device_id::text,p.server_id::text,s.name,p.revision,p.address,p.public_key,p.state,p.desired,p.created_at,s.vpn_error<>'' FROM profiles p JOIN devices d ON d.id=p.device_id JOIN servers s ON s.id=p.server_id WHERE (d.user_id=$1 OR $2) AND p.state<>'revoked' ORDER BY p.revision DESC`, u.ID, u.Role == "admin")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Profile{}
	for rows.Next() {
		var p Profile
		if err = rows.Scan(&p.ID, &p.DeviceID, &p.ServerID, &p.ServerName, &p.Revision, &p.Address, &p.PublicKey, &p.State, &p.Desired, &p.CreatedAt, &p.NeedsAttention); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) Download(ctx context.Context, u User, id string) (Profile, Target, error) {
	var p Profile
	var t Target
	err := s.Pool.QueryRow(ctx, `SELECT p.id::text,p.device_id::text,p.server_id::text,p.address,p.encrypted_keys,s.endpoint,s.vpn_port,s.encrypted_vpn_settings FROM profiles p JOIN devices d ON d.id=p.device_id JOIN servers s ON s.id=p.server_id WHERE p.id=$1 AND (d.user_id=$2 OR $3) AND p.state='ready' AND p.desired`, id, u.ID, u.Role == "admin").Scan(&p.ID, &p.DeviceID, &p.ServerID, &p.Address, &p.EncryptedKeys, &t.Endpoint, &t.VPNPort, &t.EncryptedSettings)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	t.ID = p.ServerID
	return p, t, err
}
func (s *Store) Target(ctx context.Context, id string) (Target, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return Target{}, err
	}
	defer tx.Rollback(ctx)
	var t Target
	err = tx.QueryRow(ctx, `SELECT s.id::text,s.hostname,s.ssh_port,s.ssh_user,s.host_key_fingerprint,COALESCE(k.encrypted_private_key,s.encrypted_private_key),s.encrypted_vpn_settings,s.endpoint,s.vpn_port,s.desired_generation,s.ssh_auth_method,COALESCE(s.ssh_key_id::text,'') FROM servers s LEFT JOIN ssh_keys k ON k.id=s.ssh_key_id WHERE s.id=$1`, id).Scan(&t.ID, &t.Hostname, &t.SSHPort, &t.SSHUser, &t.Fingerprint, &t.EncryptedKey, &t.EncryptedSettings, &t.Endpoint, &t.VPNPort, &t.Generation, &t.AuthMethod, &t.SSHKeyID)
	if err != nil {
		return t, err
	}
	rows, err := tx.Query(ctx, "SELECT id::text,device_id::text,public_key,address,encrypted_keys,desired FROM profiles WHERE server_id=$1 AND state<>'revoked' ORDER BY id", id)
	if err != nil {
		return t, err
	}
	for rows.Next() {
		var p Profile
		p.ServerID = id
		if err = rows.Scan(&p.ID, &p.DeviceID, &p.PublicKey, &p.Address, &p.EncryptedKeys, &p.Desired); err != nil {
			rows.Close()
			return t, err
		}
		t.Profiles = append(t.Profiles, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return t, err
	}
	return t, tx.Commit(ctx)
}
func (s *Store) Jobs(ctx context.Context) ([]Job, error) {
	rows, err := s.Pool.Query(ctx, "SELECT id::text,server_id::text,kind,status,stage,attempts,error,created_at FROM jobs ORDER BY created_at DESC LIMIT 100")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		var j Job
		if err = rows.Scan(&j.ID, &j.ServerID, &j.Kind, &j.Status, &j.Stage, &j.Attempts, &j.Error, &j.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
func (s *Store) Claim(ctx context.Context) (Job, error) {
	var j Job
	err := s.Pool.QueryRow(ctx, `UPDATE jobs SET status='running',stage='connecting',started_at=now(),attempts=attempts+1,run_token=gen_random_uuid() WHERE id=(SELECT id FROM jobs WHERE status='queued' AND run_after<=now() ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING id::text,server_id::text,kind,attempts,run_token::text`).Scan(&j.ID, &j.ServerID, &j.Kind, &j.Attempts, &j.RunToken)
	return j, err
}
func (s *Store) Finish(ctx context.Context, j Job, t Target, failure string) error {
	tx, err := vpnTx(ctx, s)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var active string
	err = tx.QueryRow(ctx, "SELECT id::text FROM jobs WHERE id=$1 AND run_token=$2 AND status='running' FOR UPDATE", j.ID, j.RunToken).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}

	if failure != "" {
		// Retry transient failures, then leave a visible failed job for manual retry.
		status := "failed"
		var pending bool
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM jobs WHERE server_id=$1 AND status='queued')", j.ServerID).Scan(&pending); err != nil {
			return err
		}
		if j.Attempts < 3 && !pending {
			status = "queued"
		}
		_, err = tx.Exec(ctx, "UPDATE jobs SET status=$2,stage=$2,error=$3,finished_at=now(),run_after=now()+interval '30 seconds' WHERE id=$1", j.ID, status, failure)
		if err != nil {
			return err
		}
		if status == "failed" && !pending {
			if _, err = tx.Exec(ctx, `INSERT INTO notifications(user_id,event_key,kind,severity,server_id) SELECT id,'job:'||$1::text,'job_failed','warning',$2 FROM users WHERE role='admin' ON CONFLICT DO NOTHING`, j.ID, j.ServerID); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, "UPDATE servers SET vpn_error=$2 WHERE id=$1", j.ServerID, failure)
	} else {
		_, err = tx.Exec(ctx, "UPDATE jobs SET status='succeeded',stage='done',error='',finished_at=now() WHERE id=$1", j.ID)
		if err != nil {
			return err
		}
		if j.Kind == "check" {
			_, err = tx.Exec(ctx, "UPDATE servers SET checked_at=now(),vpn_error='',vpn_state=CASE WHEN vpn_state='new' THEN 'checked' ELSE vpn_state END WHERE id=$1", j.ServerID)
		} else {
			_, err = tx.Exec(ctx, "UPDATE servers SET vpn_state='ready',vpn_error='',applied_generation=$2,checked_at=now() WHERE id=$1", j.ServerID, t.Generation)
			if err != nil {
				return err
			}
			for _, p := range t.Profiles {
				state := "revoked"
				if p.Desired {
					state = "ready"
				}
				if _, err = tx.Exec(ctx, "UPDATE profiles SET state=$2,applied_at=now() WHERE id=$1 AND desired=$3", p.ID, state, p.Desired); err != nil {
					return err
				}
			}
		}
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
