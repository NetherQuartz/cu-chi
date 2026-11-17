package store

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NetherQuartz/cu-chi/internal/telemetry"
	"github.com/jackc/pgx/v5"
	"time"
)

type Monitor struct {
	ServerID    string            `json:"server_id"`
	AttemptedAt *time.Time        `json:"attempted_at"`
	SucceededAt *time.Time        `json:"succeeded_at"`
	Error       string            `json:"error"`
	Failures    int               `json:"failures"`
	Latest      *telemetry.Point  `json:"latest"`
	History     []telemetry.Point `json:"history,omitempty"`
}

func (s *Store) NextMonitor(ctx context.Context) (string, error) {
	var id string
	err := s.Pool.QueryRow(ctx, `SELECT s.id::text FROM servers s LEFT JOIN monitor_state m ON m.server_id=s.id WHERE s.applied_generation>0 AND (m.attempted_at IS NULL OR m.attempted_at<now()-interval '60 seconds') AND NOT EXISTS(SELECT 1 FROM jobs j WHERE j.server_id=s.id AND j.status IN ('queued','running')) ORDER BY m.attempted_at NULLS FIRST LIMIT 1`).Scan(&id)
	return id, err
}
func (s *Store) SaveObservation(ctx context.Context, id string, v telemetry.Sample, failure string) error {
	// PostgreSQL timestamptz stores microseconds. Normalize before ordering and
	// duplicate detection so retrying a nanosecond-resolution sample is a no-op.
	v.At = v.At.UTC().Truncate(time.Microsecond)
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO monitor_state(server_id,attempted_at) VALUES($1,$2) ON CONFLICT DO NOTHING`, id, v.At)
	if err != nil {
		return err
	}
	var oldRaw []byte
	var last *time.Time
	if err = tx.QueryRow(ctx, `SELECT sample,succeeded_at FROM monitor_state WHERE server_id=$1 FOR UPDATE`, id).Scan(&oldRaw, &last); err != nil {
		return err
	}
	if last != nil && !v.At.After(*last) {
		return nil
	}
	if failure != "" {
		_, err = tx.Exec(ctx, `UPDATE monitor_state SET attempted_at=$2,error=$3,failures=LEAST(failures+1,1000) WHERE server_id=$1`, id, v.At, failure)
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	var previous *telemetry.Sample
	if len(oldRaw) > 0 {
		previous = new(telemetry.Sample)
		if err = json.Unmarshal(oldRaw, previous); err != nil {
			return err
		}
	}
	point := telemetry.Between(v, previous)
	raw, err := json.Marshal(point)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO host_samples(server_id,at,point) VALUES($1,$2,$3)`, id, v.At, raw); err != nil {
		return err
	}
	// Public keys are resolved against this server only; synthetic/unknown peers are ignored.
	type owner struct{ profile, device, user, name string }
	owners := map[string]owner{}
	rows, err := tx.Query(ctx, `SELECT p.public_key,p.id::text,p.device_id::text,d.user_id::text,d.name FROM profiles p JOIN devices d ON d.id=p.device_id WHERE p.server_id=$1 AND p.state<>'revoked'`, id)
	if err != nil {
		return err
	}
	for rows.Next() {
		var key string
		var o owner
		if err = rows.Scan(&key, &o.profile, &o.device, &o.user, &o.name); err != nil {
			rows.Close()
			return err
		}
		owners[key] = o
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, p := range v.Peers {
		o, ok := owners[p.Key]
		if !ok {
			continue
		}
		var epoch string
		var rx, txBytes int64
		var at time.Time
		err = tx.QueryRow(ctx, `SELECT epoch,rx,tx,at FROM peer_counters WHERE profile_id=$1`, o.profile).Scan(&epoch, &rx, &txBytes, &at)
		missing := errors.Is(err, pgx.ErrNoRows)
		if err != nil && !missing {
			return err
		}
		currentEpoch := v.Boot + ":" + v.Epoch
		up, down, gap := telemetry.Delta(p.RX, p.TX, rx, txBytes, !missing && epoch == currentEpoch)
		gap = gap || v.At.Sub(at) > 3*time.Minute
		var handshake *time.Time
		if p.Handshake > 0 {
			h := time.Unix(p.Handshake, 0).UTC()
			handshake = &h
		}
		if _, err = tx.Exec(ctx, `INSERT INTO peer_counters(profile_id,epoch,rx,tx,at,handshake) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(profile_id) DO UPDATE SET epoch=excluded.epoch,rx=excluded.rx,tx=excluded.tx,at=excluded.at,handshake=excluded.handshake`, o.profile, currentEpoch, p.RX, p.TX, v.At, handshake); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO traffic_totals(device_id,server_id,user_id,device_name,upload,download,first_at,last_at,handshake) VALUES($1,$2,$3,$4,$5,$6,$7,$7,$8) ON CONFLICT(device_id,server_id) DO UPDATE SET device_name=excluded.device_name,upload=traffic_totals.upload+excluded.upload,download=traffic_totals.download+excluded.download,last_at=excluded.last_at,handshake=GREATEST(traffic_totals.handshake,excluded.handshake)`, o.device, id, o.user, o.name, up, down, v.At, handshake); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO traffic_samples(device_id,server_id,at,upload,download,gap,seconds) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(device_id,server_id,at) DO UPDATE SET upload=traffic_samples.upload+excluded.upload,download=traffic_samples.download+excluded.download,gap=traffic_samples.gap OR excluded.gap`, o.device, id, v.At, up, down, gap, max(0, v.At.Sub(at).Seconds())); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE monitor_state SET attempted_at=$2,succeeded_at=$2,error='',failures=0,sample=$3 WHERE server_id=$1`, id, v.At, raw); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) Monitors(ctx context.Context) ([]Monitor, error) {
	rows, err := s.Pool.Query(ctx, `SELECT s.id::text,m.attempted_at,m.succeeded_at,COALESCE(m.error,''),COALESCE(m.failures,0),m.sample FROM servers s LEFT JOIN monitor_state m ON s.id=m.server_id ORDER BY s.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Monitor{}
	for rows.Next() {
		var m Monitor
		var raw []byte
		if err = rows.Scan(&m.ServerID, &m.AttemptedAt, &m.SucceededAt, &m.Error, &m.Failures, &raw); err != nil {
			return nil, err
		}
		if len(raw) > 0 {
			m.Latest = new(telemetry.Point)
			if err = json.Unmarshal(raw, m.Latest); err != nil {
				return nil, err
			}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func (s *Store) HostHistory(ctx context.Context, id string, since time.Time) ([]telemetry.Point, error) {
	rows, err := s.Pool.Query(ctx, `SELECT point FROM host_samples WHERE server_id=$1 AND at>=$2 ORDER BY at`, id, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []telemetry.Point{}
	for rows.Next() {
		var raw []byte
		var p telemetry.Point
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) SetLocation(ctx context.Context, u User, id, country, city string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	result, err := tx.Exec(ctx, `UPDATE servers SET country_code=$2,city=$3 WHERE id=$1`, id, country, city)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrNotFound
	}
	if err = audit(ctx, tx, u.ID, "server.location_changed", id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type TrafficFilter struct {
	UserID, DeviceID, ServerID string
	Since                      time.Time
	Bucket                     int
}
type TrafficRow struct {
	UploadRate       *float64   `json:"upload_rate"`
	DownloadRate     *float64   `json:"download_rate"`
	UserID           string     `json:"user_id"`
	Username         string     `json:"username"`
	DeviceID         string     `json:"device_id"`
	DeviceName       string     `json:"device_name"`
	ServerID         string     `json:"server_id"`
	ServerName       string     `json:"server_name"`
	Country          string     `json:"country_code"`
	City             string     `json:"city"`
	Upload           int64      `json:"upload"`
	Download         int64      `json:"download"`
	LifetimeUpload   int64      `json:"lifetime_upload"`
	LifetimeDownload int64      `json:"lifetime_download"`
	FirstAt          time.Time  `json:"first_at"`
	LastAt           time.Time  `json:"last_at"`
	Handshake        *time.Time `json:"handshake"`
	Gaps             int        `json:"gaps"`
}
type TrafficPoint struct {
	At       time.Time `json:"at"`
	Upload   int64     `json:"upload"`
	Download int64     `json:"download"`
	Gaps     int       `json:"gaps"`
	Samples  int       `json:"samples"`
}
type Traffic struct {
	Rows    []TrafficRow   `json:"rows"`
	History []TrafficPoint `json:"history"`
	Since   time.Time      `json:"since"`
	Bucket  int            `json:"bucket_seconds"`
}

const trafficWhere = ` WHERE ($2 OR t.user_id=$1::uuid) AND ($3='' OR t.user_id=NULLIF($3,'')::uuid) AND ($4='' OR t.device_id=NULLIF($4,'')::uuid) AND ($5='' OR t.server_id=NULLIF($5,'')::uuid) `

func (s *Store) Traffic(ctx context.Context, u User, f TrafficFilter) (Traffic, error) {
	out := Traffic{Rows: []TrafficRow{}, History: []TrafficPoint{}, Since: f.Since, Bucket: f.Bucket}
	args := []any{u.ID, u.Role == "admin", f.UserID, f.DeviceID, f.ServerID, f.Since}
	rows, err := s.Pool.Query(ctx, `SELECT t.user_id::text,u.username,t.device_id::text,t.device_name,t.server_id::text,s.name,s.country_code,s.city,t.upload,t.download,t.first_at,t.last_at,t.handshake,COALESCE(sum(p.upload),0)::bigint,COALESCE(sum(p.download),0)::bigint,count(*) FILTER(WHERE p.gap)::int,(SELECT CASE WHEN NOT x.gap AND x.seconds>0 AND x.at>now()-interval '3 minutes' THEN x.upload/x.seconds END FROM traffic_samples x WHERE x.device_id=t.device_id AND x.server_id=t.server_id ORDER BY x.at DESC LIMIT 1),(SELECT CASE WHEN NOT x.gap AND x.seconds>0 AND x.at>now()-interval '3 minutes' THEN x.download/x.seconds END FROM traffic_samples x WHERE x.device_id=t.device_id AND x.server_id=t.server_id ORDER BY x.at DESC LIMIT 1) FROM traffic_totals t JOIN users u ON u.id=t.user_id JOIN servers s ON s.id=t.server_id LEFT JOIN traffic_samples p ON p.device_id=t.device_id AND p.server_id=t.server_id AND p.at>=$6`+trafficWhere+` GROUP BY t.device_id,t.server_id,u.username,s.name,s.country_code,s.city ORDER BY t.last_at DESC`, args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var r TrafficRow
		if err = rows.Scan(&r.UserID, &r.Username, &r.DeviceID, &r.DeviceName, &r.ServerID, &r.ServerName, &r.Country, &r.City, &r.LifetimeUpload, &r.LifetimeDownload, &r.FirstAt, &r.LastAt, &r.Handshake, &r.Upload, &r.Download, &r.Gaps, &r.UploadRate, &r.DownloadRate); err != nil {
			rows.Close()
			return out, err
		}
		out.Rows = append(out.Rows, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	args = append(args, f.Bucket)
	rows, err = s.Pool.Query(ctx, `SELECT to_timestamp(floor(extract(epoch FROM p.at)/$7)*$7),sum(p.upload)::bigint,sum(p.download)::bigint,count(*) FILTER(WHERE p.gap)::int,count(*)::int FROM traffic_samples p JOIN traffic_totals t USING(device_id,server_id)`+trafficWhere+` AND p.at>=$6 GROUP BY 1 ORDER BY 1`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var p TrafficPoint
		if err = rows.Scan(&p.At, &p.Upload, &p.Download, &p.Gaps, &p.Samples); err != nil {
			return out, err
		}
		out.History = append(out.History, p)
	}
	return out, rows.Err()
}

type Service struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Country     string     `json:"country_code"`
	City        string     `json:"city"`
	Default     bool       `json:"is_default"`
	CheckedAt   *time.Time `json:"checked_at"`
	SucceededAt *time.Time `json:"succeeded_at"`
	Tunnel      *bool      `json:"tunnel"`
	Failures    int        `json:"failures"`
}

func (s *Store) Services(ctx context.Context, u User) ([]Service, error) {
	rows, err := s.Pool.Query(ctx, `SELECT s.id::text,s.name,s.country_code,s.city,COALESCE(v.default_server_id=s.id,false),m.attempted_at,m.succeeded_at,(m.sample->>'tunnel')::boolean,COALESCE(m.failures,0) FROM servers s CROSS JOIN vpn_preferences v LEFT JOIN monitor_state m ON m.server_id=s.id WHERE s.id=v.default_server_id OR EXISTS(SELECT 1 FROM profiles p JOIN devices d ON p.device_id=d.id WHERE p.server_id=s.id AND p.state<>'revoked' AND (d.user_id=$1 OR $2)) ORDER BY s.name`, u.ID, u.Role == "admin")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Service{}
	for rows.Next() {
		var v Service
		if err = rows.Scan(&v.ID, &v.Name, &v.Country, &v.City, &v.Default, &v.CheckedAt, &v.SucceededAt, &v.Tunnel, &v.Failures); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
