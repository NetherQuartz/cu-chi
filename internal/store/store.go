package store

import (
	"context"
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS
var ErrNotFound = errors.New("not found")

type Store struct{ Pool *pgxpool.Pool }
type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}
type Session struct {
	User    User
	CSRF    string
	Expires time.Time
}
type Device struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Owner     string    `json:"owner"`
	Name      string    `json:"name"`
	Platform  string    `json:"platform"`
	CreatedAt time.Time `json:"created_at"`
}
type Server struct {
	HostKeyPolicy string    `json:"host_key_policy"`
	AuthMethod    string    `json:"ssh_auth_method"`
	SSHKeyID      string    `json:"ssh_key_id,omitempty"`
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Hostname      string    `json:"hostname"`
	SSHPort       int       `json:"ssh_port"`
	SSHUser       string    `json:"ssh_user"`
	Fingerprint   string    `json:"host_key_fingerprint"`
	CreatedAt     time.Time `json:"created_at"`
	Endpoint      string    `json:"endpoint"`
	VPNPort       int       `json:"vpn_port"`
	VPNState      string    `json:"vpn_state"`
	VPNError      string    `json:"vpn_error"`
	Default       bool      `json:"is_default"`
}
type Event struct {
	ID         int64     `json:"id"`
	Actor      string    `json:"actor"`
	Action     string    `json:"action"`
	ResourceID string    `json:"resource_id"`
	CreatedAt  time.Time `json:"created_at"`
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("invalid database configuration")
	}
	cfg.MaxConns = 8
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err = p.Ping(ctx); err != nil {
		p.Close()
		return nil, err
	}
	s := &Store{p}
	if err = s.migrate(ctx); err != nil {
		p.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) migrate(ctx context.Context) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(718352104)"); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations (name text PRIMARY KEY, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now())"); err != nil {
		return err
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		sql, e := migrations.ReadFile("migrations/" + entry.Name())
		if e != nil {
			return e
		}
		checksum := fmt.Sprintf("%x", sha256.Sum256(sql))
		var existing string
		e = tx.QueryRow(ctx, "SELECT checksum FROM schema_migrations WHERE name=$1", entry.Name()).Scan(&existing)
		if e == nil {
			if existing != checksum {
				return fmt.Errorf("migration %s was changed after application", entry.Name())
			}
			continue
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if _, e = tx.Exec(ctx, string(sql)); e != nil {
			return fmt.Errorf("migration %s: %w", entry.Name(), e)
		}
		if _, e = tx.Exec(ctx, "INSERT INTO schema_migrations(name,checksum) VALUES($1,$2)", entry.Name(), checksum); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
func (s *Store) UpsertUser(ctx context.Context, issuer, subject, username, role string) (User, error) {
	var u User
	err := s.Pool.QueryRow(ctx, `INSERT INTO users(issuer,subject,username,role) VALUES($1,$2,$3,$4) ON CONFLICT(issuer,subject) DO UPDATE SET username=excluded.username, role=excluded.role, last_login_at=now() RETURNING id::text,username,role`, issuer, subject, username, role).Scan(&u.ID, &u.Username, &u.Role)
	return u, err
}
func (s *Store) CreateSession(ctx context.Context, tokenHash, userID, csrf string, expires time.Time) error {
	_, err := s.Pool.Exec(ctx, "INSERT INTO sessions(token_hash,user_id,csrf_token,expires_at) VALUES($1,$2,$3,$4)", tokenHash, userID, csrf, expires)
	return err
}
func (s *Store) Session(ctx context.Context, tokenHash string) (Session, error) {
	var v Session
	err := s.Pool.QueryRow(ctx, `SELECT u.id::text,u.username,u.role,s.csrf_token,s.expires_at FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.expires_at>now() AND u.role <> 'denied'`, tokenHash).Scan(&v.User.ID, &v.User.Username, &v.User.Role, &v.CSRF, &v.Expires)
	return v, err
}
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.Pool.Exec(ctx, "DELETE FROM sessions WHERE token_hash=$1", tokenHash)
	return err
}
func (s *Store) SaveLogin(ctx context.Context, stateHash string, payload []byte) error {
	_, err := s.Pool.Exec(ctx, "INSERT INTO login_attempts(state_hash,encrypted_payload,expires_at) VALUES($1,$2,now()+interval '5 minutes')", stateHash, payload)
	return err
}
func (s *Store) ConsumeLogin(ctx context.Context, stateHash string) ([]byte, error) {
	var v []byte
	err := s.Pool.QueryRow(ctx, "DELETE FROM login_attempts WHERE state_hash=$1 AND expires_at>now() RETURNING encrypted_payload", stateHash).Scan(&v)
	return v, err
}
func (s *Store) Cleanup(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, "DELETE FROM sessions WHERE expires_at <= now(); DELETE FROM login_attempts WHERE expires_at <= now()")
	return err
}
func (s *Store) Devices(ctx context.Context, userID string, all bool) ([]Device, error) {
	rows, err := s.Pool.Query(ctx, `SELECT d.id::text,d.user_id::text,u.username,d.name,d.platform,d.created_at FROM devices d JOIN users u ON u.id=d.user_id WHERE ($2 OR d.user_id=$1::uuid) ORDER BY d.created_at DESC`, userID, all)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		var d Device
		if err := rows.Scan(&d.ID, &d.UserID, &d.Owner, &d.Name, &d.Platform, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func audit(ctx context.Context, tx pgx.Tx, actor, action, resource string) error {
	_, err := tx.Exec(ctx, "INSERT INTO audit_events(actor_id,action,resource_id) VALUES($1,$2,$3)", actor, action, resource)
	return err
}
func (s *Store) CreateDevice(ctx context.Context, u User, name, platform string) (Device, error) {
	d := Device{UserID: u.ID, Owner: u.Username, Name: name, Platform: platform}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return d, err
	}
	defer tx.Rollback(ctx)
	if err = tx.QueryRow(ctx, "INSERT INTO devices(user_id,name,platform) VALUES($1,$2,$3) RETURNING id::text,created_at", u.ID, name, platform).Scan(&d.ID, &d.CreatedAt); err != nil {
		return d, err
	}
	if err = audit(ctx, tx, u.ID, "device.created", d.ID); err != nil {
		return d, err
	}
	return d, tx.Commit(ctx)
}
func (s *Store) DeleteDevice(ctx context.Context, u User, id string) error {
	tx, err := vpnTx(ctx, s)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var found string
	err = tx.QueryRow(ctx, "SELECT id::text FROM devices WHERE id=$1 AND (user_id=$2 OR $3) FOR UPDATE", id, u.ID, u.Role == "admin").Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	var active bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM profiles WHERE device_id=$1 AND state<>'revoked')", id).Scan(&active); err != nil {
		return err
	}
	if active {
		return ErrConflict
	}
	if _, err = tx.Exec(ctx, "DELETE FROM profiles WHERE device_id=$1", id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "DELETE FROM devices WHERE id=$1", id); err != nil {
		return err
	}
	if err = audit(ctx, tx, u.ID, "device.deleted", id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) Servers(ctx context.Context) ([]Server, error) {
	rows, err := s.Pool.Query(ctx, "SELECT s.id::text,s.name,s.hostname,s.ssh_port,s.ssh_user,s.host_key_fingerprint,s.created_at,s.endpoint,s.vpn_port,s.vpn_state,s.vpn_error,s.host_key_policy,s.ssh_auth_method,COALESCE(s.ssh_key_id::text,''),COALESCE(v.default_server_id=s.id,false) FROM servers s CROSS JOIN vpn_preferences v ORDER BY s.created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Server{}
	for rows.Next() {
		var v Server
		if err := rows.Scan(&v.ID, &v.Name, &v.Hostname, &v.SSHPort, &v.SSHUser, &v.Fingerprint, &v.CreatedAt, &v.Endpoint, &v.VPNPort, &v.VPNState, &v.VPNError, &v.HostKeyPolicy, &v.AuthMethod, &v.SSHKeyID, &v.Default); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) CreateServer(ctx context.Context, u User, v Server, encryptedKey []byte) (Server, error) {
	if v.HostKeyPolicy == "" {
		v.HostKeyPolicy = "manual"
	}
	if v.AuthMethod == "" {
		v.AuthMethod = "key"
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return v, err
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `INSERT INTO servers(id,name,hostname,ssh_port,ssh_user,host_key_fingerprint,encrypted_private_key,created_by,ssh_auth_method,ssh_key_id,host_key_policy) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,'')::uuid,$11) RETURNING created_at`, v.ID, v.Name, v.Hostname, v.SSHPort, v.SSHUser, v.Fingerprint, encryptedKey, u.ID, v.AuthMethod, v.SSHKeyID, v.HostKeyPolicy).Scan(&v.CreatedAt)
	if err != nil {
		return v, err
	}
	if err = audit(ctx, tx, u.ID, "server.registered", v.ID); err != nil {
		return v, err
	}
	return v, tx.Commit(ctx)
}
func (s *Store) Events(ctx context.Context) ([]Event, error) {
	rows, err := s.Pool.Query(ctx, `SELECT a.id,u.username,a.action,a.resource_id,a.created_at FROM audit_events a JOIN users u ON u.id=a.actor_id ORDER BY a.id DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var v Event
		if err := rows.Scan(&v.ID, &v.Actor, &v.Action, &v.ResourceID, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
