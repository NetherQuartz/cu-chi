package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/NetherQuartz/cu-chi/internal/awg"
	"github.com/NetherQuartz/cu-chi/internal/cryptobox"
	"github.com/NetherQuartz/cu-chi/internal/deployer"
	"github.com/NetherQuartz/cu-chi/internal/store"
	"github.com/NetherQuartz/cu-chi/internal/telemetry"
	"github.com/jackc/pgx/v5"
)

type Remote interface {
	Check(context.Context, deployer.Credentials) error
	Apply(context.Context, deployer.Credentials, deployer.Payload) error
}
type Worker struct {
	DB     *store.Store
	Box    *cryptobox.Box
	Remote Remote
	Image  string
}

func (w *Worker) Run(ctx context.Context) {
	for ctx.Err() == nil {
		w.lead(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}
func (w *Worker) lead(ctx context.Context) {
	conn, err := w.DB.Pool.Acquire(ctx)
	if err != nil {
		return
	}
	defer conn.Release()
	var locked bool
	if conn.QueryRow(ctx, "SELECT pg_try_advisory_lock(hashtextextended(current_schema(),718352106))").Scan(&locked) != nil || !locked {
		return
	}
	// This session lock survives individual transactions. Closing the DB connection releases it.
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := conn.Exec(c, "SELECT pg_advisory_unlock(hashtextextended(current_schema(),718352106))"); err != nil {
			conn.Conn().Close(c)
		}
	}()
	// A crashed running job must not disappear or override a newer queued change.
	_, err = conn.Exec(ctx, `UPDATE jobs j SET status=CASE WHEN EXISTS(SELECT 1 FROM jobs q WHERE q.server_id=j.server_id AND q.status='queued') THEN 'failed' ELSE 'queued' END,stage='interrupted',error='Worker restarted; reconciliation queued',run_after=now() WHERE status='running'`)
	if err != nil {
		return
	}
	for ctx.Err() == nil {
		// Check lock connection health before each side effect. Remote flock and generation fencing
		// also prevent an old command overwriting a newer configuration after a network partition.
		if conn.Ping(ctx) != nil {
			return
		}
		j, err := w.DB.Claim(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			w.observeNext(ctx)
			w.probeNext(ctx)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		if err != nil {
			slog.Warn("VPN queue unavailable")
			return
		}
		op, cancel := context.WithTimeout(ctx, 20*time.Minute)
		t, err := w.DB.Target(op, j.ServerID)
		if err == nil {
			if j.Kind != "check" && t.Generation > 0 {
				w.observe(op, t)
			}
			err = w.Execute(op, j, t)
		}
		cancel()
		failure := ""
		if err != nil {
			failure = err.Error()
			slog.Warn("VPN job failed", "job", j.ID, "reason", failure)
		}
		finish, done := context.WithTimeout(context.Background(), 10*time.Second)
		if err = w.DB.Finish(finish, j, t, failure); err != nil {
			slog.Warn("VPN job result could not be saved", "job", j.ID)
			done()
			return
		}
		done()
	}
}
func (w *Worker) Execute(ctx context.Context, j store.Job, t store.Target) error {
	namespace := "server:" + t.ID
	if t.AuthMethod == "saved_key" {
		namespace = "ssh-key:" + t.SSHKeyID
	}
	key, err := w.Box.Open(t.EncryptedKey, namespace)
	if err != nil {
		return errors.New("cannot decrypt SSH credentials")
	}
	c := deployer.Credentials{Host: t.Hostname, Port: t.SSHPort, User: t.SSHUser, Fingerprint: t.Fingerprint, Key: key}
	if t.AuthMethod == "password" {
		c.AuthMethod = "password"
		c.Password = key
		c.Key = nil
	}
	defer clear(key)
	if j.Kind == "check" {
		return w.Remote.Check(ctx, c)
	}
	if !deployer.ValidImage(w.Image) {
		return errors.New("AWG runtime image is not configured")
	}
	settings, err := w.Box.Open(t.EncryptedSettings, "vpn:"+t.ID)
	if err != nil {
		return errors.New("cannot decrypt VPN settings")
	}
	defer clear(settings)
	var s awg.Settings
	if json.Unmarshal(settings, &s) != nil {
		return errors.New("invalid VPN settings")
	}
	peers := []awg.Peer{}
	public := []string{}
	for _, p := range t.Profiles {
		if !p.Desired {
			continue
		}
		data, err := w.Box.Open(p.EncryptedKeys, "profile:"+p.ID)
		if err != nil {
			return errors.New("cannot decrypt profile")
		}
		var k awg.Keys
		err = json.Unmarshal(data, &k)
		clear(data)
		if err != nil {
			return errors.New("invalid profile keys")
		}
		peers = append(peers, awg.Peer{Public: k.Public, PSK: k.PSK, Address: p.Address})
		public = append(public, k.Public)
	}
	probe := awg.NewKeys()
	p := deployer.Payload{ServerID: t.ID, Image: w.Image, Port: t.VPNPort, Generation: t.Generation, Config: s.Server(peers), WithProbe: s.Server(append(peers, awg.Peer{Public: probe.Public, PSK: probe.PSK, Address: awg.ProbeAddress})), Probe: s.Probe(probe), PublicKeys: public, Install: j.Kind == "deploy"}
	return w.Remote.Apply(ctx, c, p)
}

// Monitoring shares the existing worker leadership and yields to queued changes.
// It is read-only and cannot restart services or replace peers.
func (w *Worker) observeNext(ctx context.Context) {
	if _, ok := w.Remote.(interface {
		Observe(context.Context, deployer.Credentials, string) (telemetry.Sample, error)
	}); !ok {
		return
	}
	id, err := w.DB.NextMonitor(ctx)
	if err != nil {
		return
	}
	t, err := w.DB.Target(ctx, id)
	if err != nil {
		return
	}
	w.observe(ctx, t)
}
func (w *Worker) observe(ctx context.Context, t store.Target) {
	remote, ok := w.Remote.(interface {
		Observe(context.Context, deployer.Credentials, string) (telemetry.Sample, error)
	})
	if !ok {
		return
	}
	op, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	namespace := "server:" + t.ID
	if t.AuthMethod == "saved_key" {
		namespace = "ssh-key:" + t.SSHKeyID
	}
	key, err := w.Box.Open(t.EncryptedKey, namespace)
	if err != nil {
		save, done := context.WithTimeout(ctx, 5*time.Second)
		defer done()
		_ = w.DB.SaveObservation(save, t.ID, telemetry.Sample{At: time.Now()}, "credentials_unavailable")
		return
	}
	defer clear(key)
	c := deployer.Credentials{Host: t.Hostname, Port: t.SSHPort, User: t.SSHUser, Fingerprint: t.Fingerprint, Key: key}
	if t.AuthMethod == "password" {
		c.AuthMethod = "password"
		c.Password = key
		c.Key = nil
	}
	sample, err := remote.Observe(op, c, t.ID)
	failure := ""
	if err != nil {
		sample = telemetry.Sample{At: time.Now().UTC()}
		failure = "collection_failed"
		if errors.Is(err, deployer.ErrHostKey) {
			failure = "host_key_mismatch"
		}
	}
	save, done := context.WithTimeout(ctx, 5*time.Second)
	defer done()
	if w.DB.SaveObservation(save, t.ID, sample, failure) != nil {
		slog.Warn("monitoring result could not be saved")
	}
}
