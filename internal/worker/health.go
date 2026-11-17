package worker

import (
	"context"
	"encoding/json"
	"github.com/NetherQuartz/cu-chi/internal/awg"
	"github.com/NetherQuartz/cu-chi/internal/deployer"
	"log/slog"
	"time"
)

func (w *Worker) probeNext(ctx context.Context) {
	remote, ok := w.Remote.(interface {
		Probe(context.Context, deployer.Credentials, string, int64, awg.Settings) string
	})
	if !ok {
		return
	}
	id, err := w.DB.NextProbe(ctx)
	if err != nil {
		return
	}
	t, err := w.DB.Target(ctx, id)
	if err != nil {
		return
	}
	op, cancel := context.WithTimeout(ctx, 100*time.Second)
	defer cancel()
	result := "configuration"
	ns := "server:" + t.ID
	if t.AuthMethod == "saved_key" {
		ns = "ssh-key:" + t.SSHKeyID
	}
	key, err := w.Box.Open(t.EncryptedKey, ns)
	if err == nil {
		defer clear(key)
		c := deployer.Credentials{Host: t.Hostname, Port: t.SSHPort, User: t.SSHUser, Fingerprint: t.Fingerprint, Key: key}
		if t.AuthMethod == "password" {
			c.AuthMethod = "password"
			c.Password = key
			c.Key = nil
		}
		raw, e := w.Box.Open(t.EncryptedSettings, "vpn:"+t.ID)
		if e == nil {
			defer clear(raw)
			var settings awg.Settings
			if json.Unmarshal(raw, &settings) == nil {
				result = remote.Probe(op, c, id, t.Generation, settings)
			}
		}
	}
	save, done := context.WithTimeout(ctx, 5*time.Second)
	defer done()
	if w.DB.SaveProbe(save, id, t.Generation, time.Now(), result) != nil {
		slog.Warn("VPN probe result could not be saved")
	}
}
