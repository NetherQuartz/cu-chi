package store

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"time"
)

type SSHKey struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	PublicKey   string    `json:"public_key"`
	Fingerprint string    `json:"fingerprint"`
	ServerCount int       `json:"server_count"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *Store) SSHKeys(ctx context.Context) ([]SSHKey, error) {
	rows, err := s.Pool.Query(ctx, `SELECT k.id::text,k.name,k.public_key,k.fingerprint,k.created_at,(SELECT count(*) FROM servers WHERE ssh_key_id=k.id)+(SELECT count(*) FROM gateways WHERE ssh_key_id=k.id) FROM ssh_keys k ORDER BY k.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SSHKey{}
	for rows.Next() {
		var k SSHKey
		if err = rows.Scan(&k.ID, &k.Name, &k.PublicKey, &k.Fingerprint, &k.CreatedAt, &k.ServerCount); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
func (s *Store) CreateSSHKey(ctx context.Context, u User, k SSHKey, encrypted []byte) (SSHKey, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return k, err
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `INSERT INTO ssh_keys(id,name,public_key,fingerprint,encrypted_private_key,created_by) VALUES($1,$2,$3,$4,$5,$6) RETURNING created_at`, k.ID, k.Name, k.PublicKey, k.Fingerprint, encrypted, u.ID).Scan(&k.CreatedAt)
	if err != nil {
		return k, err
	}
	if err = audit(ctx, tx, u.ID, "ssh_key.created", k.ID); err != nil {
		return k, err
	}
	return k, tx.Commit(ctx)
}
func (s *Store) DeleteSSHKey(ctx context.Context, u User, id string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	result, err := tx.Exec(ctx, "DELETE FROM ssh_keys WHERE id=$1", id)
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23503" {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err = audit(ctx, tx, u.ID, "ssh_key.deleted", id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
