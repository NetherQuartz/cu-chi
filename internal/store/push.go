package store

import (
	"context"
	"errors"
	"github.com/NetherQuartz/cu-chi/internal/cryptobox"
	"github.com/jackc/pgx/v5"
	"time"
)

type PushPreferences struct {
	Outages    bool `json:"outages"`
	Recovery   bool `json:"recovery"`
	Reminders  bool `json:"reminders"`
	Operations bool `json:"operations"`
}

func (s *Store) PushIdentity(ctx context.Context, public string, encrypted []byte) (string, []byte, error) {
	if public != "" {
		if _, err := s.Pool.Exec(ctx, `INSERT INTO push_identity(public_key,encrypted_private_key) VALUES($1,$2) ON CONFLICT DO NOTHING`, public, encrypted); err != nil {
			return "", nil, err
		}
	}
	var p string
	var e []byte
	err := s.Pool.QueryRow(ctx, `SELECT public_key,encrypted_private_key FROM push_identity`).Scan(&p, &e)
	return p, e, err
}
func (s *Store) PushPreferences(ctx context.Context, u User) (PushPreferences, error) {
	p := PushPreferences{true, true, true, true}
	err := s.Pool.QueryRow(ctx, `SELECT outages,recovery,reminders,operations FROM notification_preferences WHERE user_id=$1`, u.ID).Scan(&p.Outages, &p.Recovery, &p.Reminders, &p.Operations)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return p, err
}
func (s *Store) SavePushPreferences(ctx context.Context, u User, p PushPreferences) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO notification_preferences(user_id,outages,recovery,reminders,operations) VALUES($1,$2,$3,$4,$5) ON CONFLICT(user_id) DO UPDATE SET outages=$2,recovery=$3,reminders=$4,operations=$5`, u.ID, p.Outages, p.Recovery, p.Reminders, p.Operations)
	return err
}

// The endpoint hash identifies a browser subscription without exposing its capability URL.
func (s *Store) SaveSubscription(ctx context.Context, u User, hash string, encrypt func(string) []byte) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,718352108))`, "push-subscriptions"); err != nil {
		return err
	}
	var id, owner string
	err = tx.QueryRow(ctx, `SELECT id::text,user_id::text FROM push_subscriptions WHERE endpoint_hash=$1 FOR UPDATE`, hash).Scan(&id, &owner)
	if err == nil && owner != u.ID {
		return ErrConflict
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if id == "" {
		var count int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM push_subscriptions WHERE user_id=$1`, u.ID).Scan(&count); err != nil {
			return err
		}
		if count >= 20 {
			return ErrConflict
		}
		id = cryptobox.ID()
	}
	_, err = tx.Exec(ctx, `INSERT INTO push_subscriptions(id,user_id,endpoint_hash,encrypted_subscription) VALUES($1,$2,$3,$4) ON CONFLICT(endpoint_hash) DO UPDATE SET encrypted_subscription=$4,updated_at=now()`, id, u.ID, hash, encrypt(id))
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) RemoveSubscription(ctx context.Context, u User, hash string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM push_subscriptions WHERE user_id=$1 AND endpoint_hash=$2`, u.ID, hash)
	return err
}
func (s *Store) TestPush(ctx context.Context, u User, hash string) error {
	var id string
	err := s.Pool.QueryRow(ctx, `SELECT id::text FROM push_subscriptions WHERE user_id=$1 AND endpoint_hash=$2`, u.ID, hash).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO notifications(user_id,event_key,kind,severity,expires_at,target_subscription_id) VALUES($1,'test:'||$2::text||':'||to_char(now() AT TIME ZONE 'UTC','YYYY-MM-DD HH24:MI'),'test','info',now()+interval '5 minutes',$2::uuid) ON CONFLICT DO NOTHING`, u.ID, id)
	return err
}

// Enforce role, preferences, incident relevance and current migration audience again
// immediately before sending. Groups are refreshed at OIDC login, as for sessions.
const pushEligible = `(n.target_subscription_id IS NULL OR n.target_subscription_id=b.id) AND u.role<>'denied' AND n.expires_at>now() AND n.read_at IS NULL AND
 CASE n.kind WHEN 'management' THEN u.role='admin' AND COALESCE(p.operations,true)
 WHEN 'job_failed' THEN u.role='admin' AND COALESCE(p.operations,true) AND n.operation<>'migration_cleanup'
 WHEN 'outage' THEN COALESCE(p.outages,true) AND (u.role='admin' OR EXISTS(SELECT 1 FROM profiles pr JOIN devices d ON d.id=pr.device_id WHERE d.user_id=u.id AND pr.server_id=n.server_id AND pr.desired))
 WHEN 'recovery' THEN COALESCE(p.recovery,true) AND EXISTS(SELECT 1 FROM health_state h WHERE h.server_id=n.server_id AND h.incident_id IS NULL AND h.result='ok' AND h.attempted_at>now()-interval '15 minutes') AND EXISTS(SELECT 1 FROM notifications prior JOIN push_outbox delivered ON delivered.notification_id=prior.id WHERE prior.user_id=u.id AND prior.incident_id=n.incident_id AND prior.kind IN ('outage','management') AND delivered.status='accepted') AND (u.role='admin' OR EXISTS(SELECT 1 FROM profiles pr JOIN devices d ON d.id=pr.device_id WHERE d.user_id=u.id AND pr.server_id=n.server_id AND pr.desired))
 WHEN 'xray_available' THEN COALESCE(p.reminders,true) AND EXISTS(SELECT 1 FROM xray_nodes x WHERE x.server_id=n.server_id AND x.published AND x.applied>0 AND x.error='')
 WHEN 'awg_profile_update' THEN COALESCE(p.reminders,true) AND EXISTS(SELECT 1 FROM profiles pr JOIN devices d ON d.id=pr.device_id JOIN servers sr ON sr.id=pr.server_id
 WHERE pr.server_id=n.server_id AND d.user_id=u.id AND pr.desired AND sr.deleted_at IS NULL AND pr.signature_revision<sr.signature_revision)
 WHEN 'reminder' THEN COALESCE(p.reminders,true) AND n.server_id=(SELECT default_server_id FROM vpn_preferences) AND EXISTS(SELECT 1 FROM profiles pr JOIN devices d ON d.id=pr.device_id WHERE d.user_id=u.id AND pr.desired AND pr.server_id<>n.server_id)
 ELSE true END AND (n.kind NOT IN ('outage','management') OR EXISTS(SELECT 1 FROM health_state h JOIN servers sr ON sr.id=h.server_id WHERE h.incident_id=n.incident_id AND h.attempted_at>now()-interval '15 minutes' AND (sr.maintenance_until IS NULL OR sr.maintenance_until<now())))`

func (s *Store) QueuePush(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO push_outbox(notification_id,subscription_id) SELECT n.id,b.id FROM notifications n JOIN users u ON u.id=n.user_id JOIN push_subscriptions b ON b.user_id=u.id LEFT JOIN notification_preferences p ON p.user_id=u.id WHERE b.created_at<=n.created_at AND `+pushEligible+` ON CONFLICT DO NOTHING`)
	return err
}

type PushJob struct {
	ID                                                    int64
	Lease, SubscriptionID, NotificationID, Kind, Severity string
	ActorNames, ServerName                                string
	Encrypted                                             []byte
	Expires                                               time.Time
	Attempts                                              int
	Eligible                                              bool
}

func (s *Store) ClaimPush(ctx context.Context) (PushJob, error) {
	var j PushJob
	lease := cryptobox.ID()
	err := s.Pool.QueryRow(ctx, `WITH candidate AS (SELECT id FROM push_outbox WHERE status IN ('pending','sending') AND next_at<=now() ORDER BY next_at,id FOR UPDATE SKIP LOCKED LIMIT 1), claimed AS (UPDATE push_outbox SET status='sending',attempts=attempts+1,lease=$1,next_at=now()+interval '2 minutes' WHERE id=(SELECT id FROM candidate) RETURNING *) SELECT c.id,c.lease::text,b.id::text,n.id::text,n.kind,n.severity,b.encrypted_subscription,n.expires_at,c.attempts,(`+pushEligible+`),CASE WHEN u.role='admin' AND n.kind='job_failed' THEN n.actor_names ELSE '' END,CASE WHEN n.kind='xray_available' OR (u.role='admin' AND n.kind IN ('job_failed','management')) THEN COALESCE((SELECT display_name FROM servers WHERE id=n.server_id),(SELECT name FROM gateways WHERE id=n.gateway_id),'') ELSE '' END FROM claimed c JOIN push_subscriptions b ON b.id=c.subscription_id JOIN notifications n ON n.id=c.notification_id JOIN users u ON u.id=n.user_id LEFT JOIN notification_preferences p ON p.user_id=u.id`, lease).Scan(&j.ID, &j.Lease, &j.SubscriptionID, &j.NotificationID, &j.Kind, &j.Severity, &j.Encrypted, &j.Expires, &j.Attempts, &j.Eligible, &j.ActorNames, &j.ServerName)
	return j, err
}
func (s *Store) FinishPush(ctx context.Context, j PushJob, code int, skip bool) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var current string
	if err = tx.QueryRow(ctx, `SELECT lease::text FROM push_outbox WHERE id=$1 AND lease=$2 AND status='sending' FOR UPDATE`, j.ID, j.Lease).Scan(&current); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if current != j.Lease {
		return nil
	}
	if code == 404 || code == 410 {
		_, err = tx.Exec(ctx, `DELETE FROM push_subscriptions WHERE id=$1`, j.SubscriptionID)
	} else {
		status := "pending"
		if skip {
			status = "skipped"
		} else if code >= 200 && code < 300 {
			status = "accepted"
		} else if j.Attempts >= 5 || (code >= 400 && code < 500 && code != 408 && code != 429) {
			status = "failed"
		}
		_, err = tx.Exec(ctx, `UPDATE push_outbox SET status=$3,last_code=$4,next_at=now()+make_interval(secs=>$5),lease=NULL WHERE id=$1 AND lease=$2`, j.ID, j.Lease, status, code, 30*(1<<min(j.Attempts, 6)))
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type PushStatus struct {
	Registered bool       `json:"registered"`
	Status     string     `json:"status"`
	At         *time.Time `json:"at"`
	Code       int        `json:"code"`
}

func (s *Store) PushStatus(ctx context.Context, u User, hash string) (PushStatus, error) {
	var out PushStatus
	var id string
	err := s.Pool.QueryRow(ctx, `SELECT id::text FROM push_subscriptions WHERE user_id=$1 AND endpoint_hash=$2`, u.ID, hash).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.Registered = true
	err = s.Pool.QueryRow(ctx, `SELECT CASE WHEN n.expires_at<now() AND COALESCE(o.status,'pending') IN ('pending','sending') THEN 'expired' ELSE COALESCE(o.status,'pending') END,n.created_at,COALESCE(o.last_code,0) FROM notifications n LEFT JOIN push_outbox o ON o.notification_id=n.id AND o.subscription_id=$1 WHERE n.user_id=$2 AND n.target_subscription_id=$1 ORDER BY n.created_at DESC LIMIT 1`, id, u.ID).Scan(&out.Status, &out.At, &out.Code)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return out, err
}
