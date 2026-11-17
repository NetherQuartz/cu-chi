package httpapp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/NetherQuartz/cu-chi/internal/cryptobox"
	"github.com/NetherQuartz/cu-chi/internal/deployer"
	"github.com/NetherQuartz/cu-chi/internal/gateway"
	"github.com/NetherQuartz/cu-chi/internal/store"
)

type fakeGateway struct {
	payload  deployer.GatewayPayload
	password string
}

func (f *fakeGateway) CheckGateway(context.Context, deployer.Credentials) error { return nil }
func (f *fakeGateway) ApplyGateway(_ context.Context, c deployer.Credentials, p deployer.GatewayPayload) error {
	f.payload = p
	f.password = string(c.Password)
	return nil
}
func TestGatewayLifecycleAndIsolation(t *testing.T) {
	a, admin, _, worker, _ := vpnSetup(t)
	ctx := context.Background()
	normal := account(t, a, "user")
	a.cfg.GatewayImage = "ghcr.io/netherquartz/cu-chi-gateway@sha256:" + strings.Repeat("b", 64)
	worker.GatewayImage = a.cfg.GatewayImage
	body := `{"name":"Test LAN gateway","hostname":"192.168.50.10","router_ip":"192.168.50.1","client_cidrs":"192.168.50.0/24","ssh_port":22,"ssh_user":"root","ssh_auth_method":"password","password":" private fixture ","host_key_policy":"manual","host_key_fingerprint":"SHA256:` + strings.Repeat("A", 43) + `"}`
	requireStatus(t, request(a, "POST", "/api/admin/gateways", normal, body), 403)
	badCSRF := admin
	badCSRF.csrf = "invalid"
	requireStatus(t, request(a, "POST", "/api/admin/gateways", badCSRF, body), 403)
	r := request(a, "POST", "/api/admin/gateways", admin, body)
	requireStatus(t, r, 201)
	var g store.Gateway
	if err := json.Unmarshal(r.Body.Bytes(), &g); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(r.Body.Bytes(), []byte("private fixture")) || bytes.Contains(r.Body.Bytes(), []byte("encrypted")) {
		t.Fatal("secret in response")
	}
	requireStatus(t, request(a, "GET", "/api/admin/gateways", normal, ""), 403)
	path := "/api/admin/gateways/" + g.ID
	requireStatus(t, request(a, "GET", path+"/keenetic.conf", normal, ""), 403)
	download := request(a, "GET", path+"/keenetic.conf", admin, "")
	if download.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatal("router config must download as a file")
	}
	requireStatus(t, download, 200)
	if download.Header().Get("Cache-Control") != "no-store" || !strings.Contains(download.Body.String(), "PrivateKey") {
		t.Fatal("missing private download protection")
	}
	requireStatus(t, request(a, "POST", path+"/deploy", admin, ""), 409)
	p := issueFor(t, a, admin, g.DeviceID)
	applyJob(t, a, worker, "")
	requireStatus(t, request(a, "POST", path+"/deploy", admin, ""), 202)
	requireStatus(t, request(a, "POST", path+"/deploy", admin, ""), 409)
	job, err := a.db.ClaimGateway(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if job.DesiredProfileID != p.ID {
		t.Fatal("wrong profile selected")
	}
	remote := &fakeGateway{}
	if err = worker.ExecuteGateway(ctx, remote, job); err != nil {
		t.Fatal(err)
	}
	if remote.password != " private fixture " {
		t.Fatal("password altered")
	}
	if _, err = gateway.ParseUplink(remote.payload.Uplink); err != nil {
		t.Fatal(err)
	}
	if err = a.db.FinishGateway(ctx, job, ""); err != nil {
		t.Fatal(err)
	}
	saved, err := a.db.Gateway(ctx, g.ID)
	if err != nil || saved.State != "ready" || saved.AppliedProfileID != p.ID {
		t.Fatalf("gateway not ready: %v", err)
	}
	// A new profile does not change the stable router identity or silently apply itself.
	p2 := issueFor(t, a, admin, g.DeviceID)
	applyJob(t, a, worker, "")
	saved, _ = a.db.Gateway(ctx, g.ID)
	if saved.CurrentProfileID != p2.ID || saved.AppliedProfileID != p.ID {
		t.Fatal("profile drift hidden")
	}
	again := request(a, "GET", path+"/keenetic.conf", admin, "")
	requireStatus(t, again, 200)
	if again.Body.String() != download.Body.String() {
		t.Fatal("LAN keys rotated with VPS profile")
	}
	requireStatus(t, request(a, "POST", path+"/deploy", admin, ""), 202)
	job2, err := a.db.ClaimGateway(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Late completion of the prior attempt cannot override the current attempt.
	if err = a.db.FinishGateway(ctx, job, "old failure"); err != nil {
		t.Fatal(err)
	}
	saved, _ = a.db.Gateway(ctx, g.ID)
	if saved.State != "running" || saved.Error != "" {
		t.Fatal("stale result overwrote current job")
	}
	requireStatus(t, request(a, "POST", "/api/devices/"+g.DeviceID+"/revoke", admin, ""), 202)
	if err = a.db.FinishGateway(ctx, job2, ""); err != nil {
		t.Fatal(err)
	}
	saved, _ = a.db.Gateway(ctx, g.ID)
	if saved.State == "ready" || saved.AppliedProfileID != p.ID {
		t.Fatal("concurrent revocation marked stale profile ready")
	}
	if err = worker.ExecuteGateway(ctx, remote, job2); err == nil {
		t.Fatal("revoked profile applied")
	}
	requireStatus(t, request(a, "DELETE", "/api/devices/"+g.DeviceID, admin, ""), 409)
	// Retry exhaustion creates one admin-only alert; stale/duplicate finishes do not.
	for attempt := 2; attempt <= 3; attempt++ {
		if _, err = a.db.Pool.Exec(ctx, `UPDATE gateways SET run_after=now() WHERE id=$1`, g.ID); err != nil {
			t.Fatal(err)
		}
		retry, e := a.db.ClaimGateway(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if retry.Attempts != attempt {
			t.Fatal("wrong retry count")
		}
		if e = a.db.FinishGateway(ctx, retry, "fixture gateway offline"); e != nil {
			t.Fatal(e)
		}
		if e = a.db.FinishGateway(ctx, retry, "fixture gateway offline"); e != nil {
			t.Fatal(e)
		}
	}
	notices, e := a.db.Inbox(ctx, admin.user)
	if e != nil {
		t.Fatal(e)
	}
	count := 0
	for _, n := range notices {
		if n.GatewayID == g.ID {
			count++
			if n.Kind != "job_failed" {
				t.Fatal("wrong gateway notice")
			}
		}
	}
	if count != 1 {
		t.Fatalf("gateway alert count %d", count)
	}
	userNotices, e := a.db.Inbox(ctx, normal.user)
	if e != nil {
		t.Fatal(e)
	}
	for _, n := range userNotices {
		if n.GatewayID != "" {
			t.Fatal("gateway alert leaked to user")
		}
	}
	requireStatus(t, request(a, "POST", path+"/check", admin, ""), 202)
	var unexpired int
	if e = a.db.Pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE gateway_id=$1 AND expires_at>now()`, g.ID).Scan(&unexpired); e != nil || unexpired != 0 {
		t.Fatal("old alert still deliverable after retry")
	}

	// Saved-key linkage is protected by the FK and counted in inventory.
	keyID := cryptobox.ID()
	_, err = a.db.CreateSSHKey(ctx, admin.user, store.SSHKey{ID: keyID, Name: "Gateway key", PublicKey: "public", Fingerprint: "fingerprint"}, []byte("encrypted fixture"))
	if err != nil {
		t.Fatal(err)
	}
	savedBody := strings.Replace(body, `"ssh_auth_method":"password","password":" private fixture "`, `"ssh_auth_method":"saved_key","ssh_key_id":"`+keyID+`"`, 1)
	savedBody = strings.Replace(savedBody, "192.168.50.10", "192.168.50.11", 1)
	savedBody = strings.Replace(savedBody, "Test LAN gateway", "Second gateway", 1)
	requireStatus(t, request(a, "POST", "/api/admin/gateways", admin, savedBody), 201)
	requireStatus(t, request(a, "DELETE", "/api/admin/ssh-keys/"+keyID, admin, ""), 409)
	keys, err := a.db.SSHKeys(ctx)
	if err != nil || len(keys) != 1 || keys[0].ServerCount != 1 {
		t.Fatal("gateway key reference not counted")
	}
}
