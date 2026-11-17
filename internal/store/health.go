package store

import (
	"context"
	"github.com/NetherQuartz/cu-chi/internal/cryptobox"
	"time"
)

type Health struct {
	ServerID         string     `json:"server_id"`
	At               *time.Time `json:"checked_at"`
	Result           string     `json:"result"`
	Failures         int        `json:"failures"`
	Successes        int        `json:"successes"`
	Incident         string     `json:"incident_id,omitempty"`
	Kind             string     `json:"incident_kind,omitempty"`
	OpenedAt         *time.Time `json:"opened_at"`
	MaintenanceUntil *time.Time `json:"maintenance_until"`
}

func (s *Store) NextProbe(ctx context.Context) (string, error) {
	var id string
	err := s.Pool.QueryRow(ctx, `SELECT s.id::text FROM servers s LEFT JOIN health_state h ON h.server_id=s.id WHERE s.applied_generation>0 AND s.desired_generation=s.applied_generation AND (s.maintenance_until IS NULL OR s.maintenance_until<now()) AND (h.attempted_at IS NULL OR h.attempted_at<now()-interval '5 minutes') AND NOT EXISTS(SELECT 1 FROM jobs j WHERE j.server_id=s.id AND j.status IN ('queued','running')) ORDER BY h.attempted_at NULLS FIRST LIMIT 1`).Scan(&id)
	return id, err
}
func (s *Store) Health(ctx context.Context, u User) ([]Health, error) {
	rows, err := s.Pool.Query(ctx, `SELECT s.id::text,h.attempted_at,COALESCE(h.result,''),COALESCE(h.failures,0),COALESCE(h.successes,0),COALESCE(h.incident_id::text,''),COALESCE(h.incident_kind,''),h.opened_at,s.maintenance_until FROM servers s LEFT JOIN health_state h ON h.server_id=s.id WHERE $2 OR EXISTS(SELECT 1 FROM profiles p JOIN devices d ON d.id=p.device_id WHERE p.server_id=s.id AND d.user_id=$1 AND p.state<>'revoked') OR s.id=(SELECT default_server_id FROM vpn_preferences)`, u.ID, u.Role == "admin")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Health{}
	for rows.Next() {
		var h Health
		if err = rows.Scan(&h.ServerID, &h.At, &h.Result, &h.Failures, &h.Successes, &h.Incident, &h.Kind, &h.OpenedAt, &h.MaintenanceUntil); err != nil {
			return nil, err
		}
		if u.Role != "admin" {
			if h.Result != "ok" && h.Result != "data" {
				h.Result = "unknown"
			}
			if h.Kind != "data" {
				h.Incident = ""
				h.Kind = ""
				h.OpenedAt = nil
			}
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
func (s *Store) SaveProbe(ctx context.Context, id string, generation int64, at time.Time, result string) error {
	at = at.UTC().Truncate(time.Microsecond)
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var eligible bool
	// Serialize against registration, maintenance and generation changes.
	if err = tx.QueryRow(ctx, `SELECT desired_generation=$2 AND applied_generation=$2 AND (maintenance_until IS NULL OR maintenance_until<$3) FROM servers WHERE id=$1 FOR UPDATE`, id, generation, at).Scan(&eligible); err != nil {
		return err
	}
	var busy bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM jobs WHERE server_id=$1 AND status IN ('queued','running'))`, id).Scan(&busy); err != nil {
		return err
	}
	if !eligible || busy {
		return nil
	}
	if _, err = tx.Exec(ctx, `INSERT INTO health_state(server_id,attempted_at,result) VALUES($1,'epoch','skipped') ON CONFLICT DO NOTHING`, id); err != nil {
		return err
	}
	var last time.Time
	var old, incident, kind string
	var failures, successes int
	if err = tx.QueryRow(ctx, `SELECT attempted_at,result,failures,successes,COALESCE(incident_id::text,''),incident_kind FROM health_state WHERE server_id=$1 FOR UPDATE`, id).Scan(&last, &old, &failures, &successes, &incident, &kind); err != nil {
		return err
	}
	if !at.After(last) {
		return nil
	}
	// An observation gap breaks consecutiveness; it cannot silently close an incident.
	if at.Sub(last) > 15*time.Minute {
		failures = 0
		successes = 0
	}
	if result == "ok" {
		successes++
		failures = 0
	} else if result == "skipped" {
		successes = 0
		failures = 0
	} else {
		successes = 0
		if old != result {
			failures = 0
		}
		failures++
	}
	if incident != "" && successes >= 2 {
		_, err = tx.Exec(ctx, `INSERT INTO notifications(user_id,event_key,kind,severity,server_id,incident_id) SELECT n.user_id,$2,'recovery','info',$1,$3 FROM notifications n JOIN users u ON u.id=n.user_id WHERE n.incident_id=$3 AND n.kind IN ('outage','management') AND u.role<>'denied' ON CONFLICT DO NOTHING`, id, incident+":recovery", incident)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE notifications SET expires_at=LEAST(expires_at,$2) WHERE incident_id=$1 AND kind IN ('outage','management')`, incident, at); err != nil {
			return err
		}
		incident = ""
		kind = ""
	}
	if failures >= 3 && (incident == "" || (kind != "data" && result == "data")) {
		// Escalate a management-only incident when a later probe proves VPN failure.
		if incident != "" {
			if _, err = tx.Exec(ctx, `UPDATE notifications SET expires_at=LEAST(expires_at,$2) WHERE incident_id=$1`, incident, at); err != nil {
				return err
			}
		}
		incident = cryptobox.ID()
		kind = result
		nKind, severity := "management", "warning"
		if result == "data" {
			nKind = "outage"
			severity = "critical"
		}
		_, err = tx.Exec(ctx, `INSERT INTO notifications(user_id,event_key,kind,severity,server_id,incident_id) SELECT u.id,$2,$3,$4,$1,$5 FROM users u WHERE u.role='admin' OR (u.role='user' AND $3='outage' AND EXISTS(SELECT 1 FROM profiles p JOIN devices d ON d.id=p.device_id WHERE p.server_id=$1 AND p.state<>'revoked' AND d.user_id=u.id)) ON CONFLICT DO NOTHING`, id, incident+":open", nKind, severity, incident)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE health_state SET opened_at=$2 WHERE server_id=$1`, id, at); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE health_state SET attempted_at=$2,result=$3,failures=LEAST($4,1000),successes=LEAST($5,1000),incident_id=NULLIF($6,'')::uuid,incident_kind=$7,opened_at=CASE WHEN $6='' THEN NULL ELSE opened_at END WHERE server_id=$1`, id, at, result, failures, successes, incident, kind)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) Maintenance(ctx context.Context, u User, id string, minutes int) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	r, err := tx.Exec(ctx, `UPDATE servers SET maintenance_until=CASE WHEN $2=0 THEN NULL ELSE now()+make_interval(mins=>$2) END WHERE id=$1`, id, minutes)
	if err != nil {
		return err
	}
	if r.RowsAffected() != 1 {
		return ErrNotFound
	}
	if _, err = tx.Exec(ctx, `UPDATE health_state SET failures=0,successes=0 WHERE server_id=$1`, id); err != nil {
		return err
	}
	if err = audit(ctx, tx, u.ID, "server.maintenance_changed", id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type Notification struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	Severity   string     `json:"severity"`
	ServerName string     `json:"server_name"`
	CreatedAt  time.Time  `json:"created_at"`
	ReadAt     *time.Time `json:"read_at"`
}

func (s *Store) Inbox(ctx context.Context, u User) ([]Notification, error) {
	rows, err := s.Pool.Query(ctx, `SELECT n.id::text,n.kind,n.severity,COALESCE(s.name,''),n.created_at,n.read_at FROM notifications n LEFT JOIN servers s ON s.id=n.server_id WHERE n.user_id=$1 AND ($2 OR n.kind NOT IN ('management','job_failed')) ORDER BY n.created_at DESC LIMIT 100`, u.ID, u.Role == "admin")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Notification{}
	for rows.Next() {
		var n Notification
		if err = rows.Scan(&n.ID, &n.Kind, &n.Severity, &n.ServerName, &n.CreatedAt, &n.ReadAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
func (s *Store) ReadNotification(ctx context.Context, u User, id string) error {
	r, err := s.Pool.Exec(ctx, `UPDATE notifications SET read_at=COALESCE(read_at,now()) WHERE id=$1 AND user_id=$2`, id, u.ID)
	if err == nil && r.RowsAffected() != 1 {
		return ErrNotFound
	}
	return err
}

// Audience is recalculated at send time. A newly imported profile cannot be inferred,
// but users who have already reissued on the current main server are excluded.
func (s *Store) ReminderAudience(ctx context.Context) ([]User, error) {
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT u.id::text,u.username,u.role FROM users u JOIN devices d ON d.user_id=u.id JOIN profiles p ON p.device_id=d.id CROSS JOIN vpn_preferences v WHERE u.role<>'denied' AND p.desired AND p.server_id<>v.default_server_id ORDER BY u.username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		var u User
		if err = rows.Scan(&u.ID, &u.Username, &u.Role); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
func (s *Store) SendReminders(ctx context.Context, actor User) (int64, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	// One reminder per user/main-server/day, including double-clicks and API retries.
	r, err := tx.Exec(ctx, `INSERT INTO notifications(user_id,event_key,kind,severity,server_id) SELECT DISTINCT u.id,'reminder:'||v.default_server_id::text||':'||to_char(now() AT TIME ZONE 'UTC','YYYY-MM-DD'),'reminder','warning',v.default_server_id FROM users u JOIN devices d ON d.user_id=u.id JOIN profiles p ON p.device_id=d.id CROSS JOIN vpn_preferences v WHERE u.role<>'denied' AND p.desired AND p.server_id<>v.default_server_id ON CONFLICT DO NOTHING`)
	if err != nil {
		return 0, err
	}
	if err = audit(ctx, tx, actor.ID, "notifications.reminded", ""); err != nil {
		return 0, err
	}
	return r.RowsAffected(), tx.Commit(ctx)
}
