package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/NetherQuartz/cu-chi/internal/awg"
	"github.com/NetherQuartz/cu-chi/internal/deployer"
	"github.com/NetherQuartz/cu-chi/internal/gateway"
	"github.com/NetherQuartz/cu-chi/internal/store"
)

type GatewayRemote interface {
	CheckGateway(context.Context, deployer.Credentials) error
	ApplyGateway(context.Context, deployer.Credentials, deployer.GatewayPayload) error
}

func (w *Worker) gatewayNext(ctx context.Context) bool {
	remote, ok := w.Remote.(GatewayRemote)
	if !ok {
		return false
	}
	g, err := w.DB.ClaimGateway(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return false
	}
	if err != nil {
		slog.Warn("Gateway queue unavailable")
		return false
	}
	op, cancel := context.WithTimeout(ctx, 20*time.Minute)
	err = w.ExecuteGateway(op, remote, g)
	cancel()
	failure := ""
	if err != nil {
		failure = err.Error()
		slog.Warn("Gateway job failed", "gateway", g.ID, "reason", failure)
	}
	finish, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	if w.DB.FinishGateway(finish, g, failure) != nil {
		slog.Warn("Gateway job result could not be saved", "gateway", g.ID)
	}
	return true
}
func (w *Worker) ExecuteGateway(ctx context.Context, remote GatewayRemote, g store.Gateway) error {
	ns := "gateway:" + g.ID
	if g.AuthMethod == "saved_key" {
		ns = "ssh-key:" + g.SSHKeyID
	}
	key, err := w.Box.Open(g.EncryptedCredential, ns)
	if err != nil {
		return errors.New("cannot decrypt gateway SSH credential")
	}
	defer clear(key)
	c := deployer.Credentials{Host: g.Hostname, Port: g.SSHPort, User: g.SSHUser, Fingerprint: g.Fingerprint, Key: key}
	if g.AuthMethod == "password" {
		c.AuthMethod = "password"
		c.Password = key
		c.Key = nil
	}
	if g.Kind == "check" {
		return remote.CheckGateway(ctx, c)
	}
	if !deployer.ValidGatewayImage(w.GatewayImage) {
		return errors.New("gateway runtime image is not configured")
	}
	p, t, err := w.DB.Download(ctx, store.User{ID: g.ID, Role: "admin"}, g.DesiredProfileID)
	if err != nil || p.DeviceID != g.DeviceID {
		return errors.New("gateway profile is no longer ready; issue and apply its current profile")
	}
	if p.SignatureRevision != g.SignatureRevision {
		return errors.New("uplink configuration changed; retry deployment")
	}
	raw, err := w.Box.Open(p.EncryptedKeys, "profile:"+p.ID)
	if err != nil {
		return errors.New("cannot decrypt gateway profile")
	}
	defer clear(raw)
	settings, err := w.Box.Open(t.EncryptedSettings, "vpn:"+t.ID)
	if err != nil {
		return errors.New("cannot decrypt uplink settings")
	}
	defer clear(settings)
	lanRaw, err := w.Box.Open(g.EncryptedLAN, "gateway-lan:"+g.ID)
	if err != nil {
		return errors.New("cannot decrypt LAN identity")
	}
	defer clear(lanRaw)
	var keys awg.Keys
	var vpn awg.Settings
	var lan gateway.LAN
	if json.Unmarshal(raw, &keys) != nil || json.Unmarshal(settings, &vpn) != nil || json.Unmarshal(lanRaw, &lan) != nil {
		return errors.New("invalid stored gateway settings")
	}
	return remote.ApplyGateway(ctx, c, deployer.GatewayPayload{ID: g.ID, Image: w.GatewayImage, LANIP: g.Hostname, RouterIP: g.RouterIP, Generation: g.Generation, LAN: lan.ServerConfig, Uplink: vpn.Client(keys, p.Address, t.Endpoint, t.VPNPort, t.Signature)})
}
