package worker

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NetherQuartz/cu-chi/internal/cryptobox"
	"github.com/NetherQuartz/cu-chi/internal/deployer"
	"github.com/NetherQuartz/cu-chi/internal/store"
	"github.com/NetherQuartz/cu-chi/internal/xray"
	"github.com/jackc/pgx/v5"
	"log/slog"
	"time"
)

type xrayRemote interface {
	ApplyXray(context.Context, deployer.Credentials, string, int64, xray.Identity, string, []string, string) (string, error)
}

func (w *Worker) xrayNext(ctx context.Context) bool {
	remote, ok := w.Remote.(xrayRemote)
	if !ok {
		return false
	}
	job, e := w.DB.XNext(ctx)
	if errors.Is(e, pgx.ErrNoRows) {
		return false
	}
	if e != nil {
		slog.Warn("Xray queue unavailable")
		return false
	}
	op, cancel := context.WithTimeout(ctx, 12*time.Minute)
	defer cancel()
	guard, lockErr := w.DB.BeginServerUse(ctx, job.ID)
	if lockErr != nil {
		return false
	}
	defer guard.Rollback(context.Background())
	sni, e := w.applyXray(op, remote, job)
	failure := ""
	if e != nil {
		failure = e.Error()
		slog.Warn("Xray apply failed", "server", job.ID, "reason", failure)
	}
	if e = w.DB.XFinish(ctx, job, sni, failure); e != nil {
		slog.Warn("Xray result could not be saved")
	}
	return true
}
func (w *Worker) applyXray(ctx context.Context, r xrayRemote, j store.XWork) (string, error) {
	t, e := w.DB.Target(ctx, j.ID)
	if e != nil {
		return "", errors.New("server credentials unavailable")
	}
	c, key, e := w.credentials(t)
	if e != nil {
		return "", e
	}
	defer clear(key)
	raw, e := w.Box.Open(j.Key, "xray-server:"+j.ID)
	if e != nil {
		return "", errors.New("Xray identity unavailable")
	}
	var k xray.Identity
	e = json.Unmarshal(raw, &k)
	clear(raw)
	if e != nil {
		return "", errors.New("invalid Xray identity")
	}
	ids := []string{}
	for _, d := range j.Clients {
		seed, e := w.Box.Open(d.Secret, "xray-device:"+d.ID)
		if e != nil {
			return "", errors.New("Xray device identity unavailable")
		}
		ids = append(ids, xray.ClientID(seed, j.ID))
		clear(seed)
	}
	return r.ApplyXray(ctx, c, j.ID, j.Revision, k, j.SNI, ids, cryptobox.ID())
}
