package store

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
)

type XNode struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Host          string `json:"-"`
	SNI           string `json:"sni"`
	Public, Short string `json:"-"`
	Revision      int64  `json:"revision"`
	Applied       int64  `json:"applied"`
	Error         string `json:"error"`
	Published     bool   `json:"published"`
}
type XDevice struct {
	ID       string `json:"device_id"`
	Desired  bool   `json:"desired"`
	Revision int64  `json:"revision"`
	Secret   []byte `json:"-"`
}

func (s *Store) XNodes(ctx context.Context) ([]XNode, error) {
	rows, e := s.Pool.Query(ctx, `SELECT s.id,s.name,s.hostname,n.sni,n.public_key,n.short_id,n.revision,n.applied,n.error,n.published FROM xray_nodes n JOIN servers s ON s.id=n.server_id ORDER BY s.created_at DESC`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []XNode{}
	for rows.Next() {
		var n XNode
		if e = rows.Scan(&n.ID, &n.Name, &n.Host, &n.SNI, &n.Public, &n.Short, &n.Revision, &n.Applied, &n.Error, &n.Published); e != nil {
			return nil, e
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
func (s *Store) XDeploy(ctx context.Context, u User, id string, key []byte, pub, short string, published bool) error {
	tx, e := vpnTx(ctx, s)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	tag, e := tx.Exec(ctx, `INSERT INTO xray_nodes(server_id,identity,public_key,short_id,published) SELECT id,$2,$3,$4,$5 FROM servers WHERE id=$1 ON CONFLICT(server_id) DO UPDATE SET revision=nextval('xray_revision'),retry_at=now(),error=''`, id, key, pub, short, published)
	if e != nil {
		return e
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if e = audit(ctx, tx, u.ID, "xray.deploy", id); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

// Announce once per server, in the same transaction that makes it available.
// The marker survives inbox retention, later reconciles, and visibility changes.
func announceXrayAvailability(ctx context.Context, tx pgx.Tx, id string) error {
	_, err := tx.Exec(ctx, `WITH first_publication AS (
   UPDATE xray_nodes SET availability_announced=true
   WHERE server_id=$1 AND published AND applied=revision AND applied>0 AND error='' AND NOT availability_announced
   RETURNING server_id
 )
 INSERT INTO notifications(user_id,event_key,kind,severity,server_id)
 SELECT u.id,'xray:available:'||n.server_id::text,'xray_available','info',n.server_id
 FROM first_publication n CROSS JOIN users u WHERE u.role IN ('admin','user')`, id)
	return err
}

func (s *Store) XPublish(ctx context.Context, id string, published bool) error {
	tx, err := vpnTx(ctx, s)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, "UPDATE xray_nodes SET published=$2 WHERE server_id=$1", id, published)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err = announceXrayAvailability(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) XDevices(ctx context.Context, u User) ([]XDevice, error) {
	rows, e := s.Pool.Query(ctx, `SELECT x.device_id,x.desired,x.revision FROM xray_devices x JOIN devices d ON d.id=x.device_id WHERE d.user_id=$1 OR $2`, u.ID, u.Role == "admin")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []XDevice{}
	for rows.Next() {
		var d XDevice
		if e = rows.Scan(&d.ID, &d.Desired, &d.Revision); e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Store) XChange(ctx context.Context, u User, id string, issue bool, secret []byte, hash string) error {
	tx, e := vpnTx(ctx, s)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var allowed bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM devices WHERE id=$1 AND platform<>'gateway' AND (user_id=$2 OR $3))`, id, u.ID, u.Role == "admin").Scan(&allowed); e != nil {
		return e
	}
	if !allowed {
		return ErrNotFound
	}
	var rev int64
	if e = tx.QueryRow(ctx, "SELECT nextval('xray_revision')").Scan(&rev); e != nil {
		return e
	}
	action := "xray.revoked_requested"
	if issue {
		var ready bool
		if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM xray_nodes WHERE applied>0 AND published)").Scan(&ready); e != nil {
			return e
		}
		if !ready {
			return ErrConflict
		}
		action = "xray.issued"
		tag, e := tx.Exec(ctx, `INSERT INTO xray_devices(device_id,secret,token_hash,revision) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, id, secret, hash, rev)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return ErrConflict
		}
	} else {
		tag, e := tx.Exec(ctx, "UPDATE xray_devices SET desired=false,revision=$2 WHERE device_id=$1 AND desired", id, rev)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return ErrConflict
		}
	}
	if _, e = tx.Exec(ctx, "UPDATE xray_nodes SET revision=$1,retry_at=now(),error=''", rev); e != nil {
		return e
	}
	if e = audit(ctx, tx, u.ID, action, id); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

type XWork struct {
	ID, SNI  string
	Revision int64
	Key      []byte
	Clients  []XDevice
}

func (s *Store) XNext(ctx context.Context) (w XWork, err error) {
	tx, e := vpnTx(ctx, s)
	if e != nil {
		return w, e
	}
	defer tx.Rollback(ctx)
	e = tx.QueryRow(ctx, `SELECT server_id,sni,revision,identity FROM xray_nodes WHERE revision>applied AND retry_at<=now() ORDER BY retry_at,server_id LIMIT 1`).Scan(&w.ID, &w.SNI, &w.Revision, &w.Key)
	if e != nil {
		return w, e
	}
	rows, e := tx.Query(ctx, "SELECT device_id,secret FROM xray_devices WHERE desired ORDER BY device_id")
	if e != nil {
		return w, e
	}
	for rows.Next() {
		var d XDevice
		if e = rows.Scan(&d.ID, &d.Secret); e != nil {
			rows.Close()
			return w, e
		}
		w.Clients = append(w.Clients, d)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return w, e
	}
	return w, tx.Commit(ctx)
}
func (s *Store) XFinish(ctx context.Context, w XWork, sni, failure string) error {
	tx, e := vpnTx(ctx, s)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if failure != "" {
		_, e = tx.Exec(ctx, "UPDATE xray_nodes SET error=$3,retry_at=now()+interval '3 minutes' WHERE server_id=$1 AND revision=$2", w.ID, w.Revision, failure)
	} else {
		_, e = tx.Exec(ctx, "UPDATE xray_nodes SET applied=GREATEST(applied,$2),sni=$3,error='' WHERE server_id=$1", w.ID, w.Revision, sni)
		if e == nil {
			e = announceXrayAvailability(ctx, tx, w.ID)
		}
		if e == nil {
			_, e = tx.Exec(ctx, "DELETE FROM xray_devices d WHERE NOT desired AND NOT EXISTS(SELECT 1 FROM xray_nodes n WHERE n.applied<d.revision)")
		}
	}
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Store) XSecret(ctx context.Context, u User, id, hash string) (XDevice, error) {
	var d XDevice
	e := s.Pool.QueryRow(ctx, `SELECT x.device_id,x.revision,x.secret FROM xray_devices x JOIN devices d ON d.id=x.device_id JOIN users u ON u.id=d.user_id WHERE x.desired AND u.role<>'denied' AND (($1<>'' AND x.token_hash=$1) OR (d.id::text=$2 AND (d.user_id::text=$3 OR $4)))`, hash, id, u.ID, u.Role == "admin").Scan(&d.ID, &d.Revision, &d.Secret)
	if errors.Is(e, pgx.ErrNoRows) {
		e = ErrNotFound
	}
	return d, e
}
