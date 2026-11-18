package httpapp

import (
	"context"
	"encoding/json"
	"github.com/NetherQuartz/cu-chi/internal/awg"
	"github.com/NetherQuartz/cu-chi/internal/cryptobox"
	"github.com/NetherQuartz/cu-chi/internal/deployer"
	"github.com/NetherQuartz/cu-chi/internal/store"
	"strings"
	"testing"
)

type warpVPN struct {
	*fakeVPN
	registrations int
	raw           string
}

func (f *warpVPN) RegisterWarp(context.Context, deployer.Credentials, string, string) (string, error) {
	f.registrations++
	return f.raw, nil
}
func TestEgressTogglePreservesProfilesAndCachesIdentity(t *testing.T) {
	a, admin, id, w, remote := vpnSetup(t)
	ctx := context.Background()
	alice := account(t, a, "user")
	d := deviceFor(t, a, alice, "Phone")
	p := issueFor(t, a, alice, d)
	applyJob(t, a, w, "")
	before := request(a, "GET", "/api/profiles/"+p.ID+"/download", alice, "")
	requireStatus(t, before, 200)
	keys, peer := awg.NewKeys(), awg.NewKeys()
	f := &warpVPN{fakeVPN: remote, raw: "[Interface]\nPrivateKey = " + keys.Private + "\nAddress = 172.16.0.2/32\n[Peer]\nPublicKey = " + peer.Public + "\nAllowedIPs = 0.0.0.0/0\nEndpoint = engage.cloudflareclient.com:2408\n"}
	w.Remote = f
	endpoint := "/api/admin/servers/" + id + "/egress"
	noCSRF := admin
	noCSRF.csrf = ""
	requireStatus(t, request(a, "POST", endpoint, noCSRF, `{"mode":"warp","accept_terms":true}`), 403)
	requireStatus(t, request(a, "GET", "/egress.js", identity{}, ""), 200)
	requireStatus(t, request(a, "POST", endpoint, alice, `{"mode":"warp","accept_terms":true}`), 403)
	for _, body := range []string{`{"mode":"warp"}`, `{"mode":"other","accept_terms":true}`} {
		requireStatus(t, request(a, "POST", endpoint, admin, body), 400)
	}
	requireStatus(t, request(a, "POST", endpoint, admin, `{"mode":"warp","accept_terms":true}`), 202)
	requireStatus(t, request(a, "POST", endpoint, admin, `{"mode":"warp","accept_terms":true}`), 409)
	target, err := a.db.Target(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if target.EgressMode != "warp" || target.AppliedEgressMode != "direct" {
		t.Fatal("requested mode presented as applied")
	}
	applyJob(t, a, w, "")
	target, err = a.db.Target(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if target.AppliedEgressMode != "warp" || strings.Contains(string(target.EncryptedWarp), keys.Private) {
		t.Fatal("mode not applied or secret stored plaintext")
	}
	if _, err = a.box.Open(target.EncryptedWarp, "warp:"+cryptobox.ID()); err == nil {
		t.Fatal("registration not server bound")
	}
	after := request(a, "GET", "/api/profiles/"+p.ID+"/download", alice, "")
	requireStatus(t, after, 200)
	if after.Body.String() != before.Body.String() {
		t.Fatal("egress switch changed client profile")
	}
	for _, mode := range []string{"direct", "warp"} {
		requireStatus(t, request(a, "POST", endpoint, admin, `{"mode":"`+mode+`","accept_terms":true}`), 202)
		applyJob(t, a, w, "")
		payload := f.payloads[len(f.payloads)-1]
		if payload.EgressMode != mode {
			t.Fatal("wrong runtime mode")
		}
		if mode == "direct" && payload.WarpConfig != "" {
			t.Fatal("direct runtime got unnecessary credentials")
		}
	}
	if f.registrations != 1 {
		t.Fatal("WARP identity regenerated")
	}
	response := request(a, "GET", "/api/admin/servers", admin, "")
	requireStatus(t, response, 200)
	if strings.Contains(response.Body.String(), keys.Private) || strings.Contains(response.Body.String(), "encrypted_warp") {
		t.Fatal("API leaked WARP registration")
	}
	var servers []store.Server
	if err = json.Unmarshal(response.Body.Bytes(), &servers); err != nil {
		t.Fatal(err)
	}
	if servers[0].EgressMode != "warp" || servers[0].AppliedEgressMode != "warp" {
		t.Fatal("API mode mismatch")
	}
	// Failed disable does not report success; explicit retry restores direct mode.
	requireStatus(t, request(a, "POST", endpoint, admin, `{"mode":"direct"}`), 202)
	f.fail = true
	applyJob(t, a, w, "test VPS offline")
	f.fail = false
	target, _ = a.db.Target(ctx, id)
	if target.AppliedEgressMode != "warp" || target.EgressMode != "direct" {
		t.Fatal("failed switch marked applied")
	}
	if _, err = a.db.Pool.Exec(ctx, "UPDATE jobs SET run_after=now() WHERE server_id=$1", id); err != nil {
		t.Fatal(err)
	}
	applyJob(t, a, w, "")
	// Another server keeps independent default and registration.
	other := cryptobox.ID()
	_, err = a.db.CreateServer(ctx, admin.user, store.Server{ID: other, Name: "Other VPS", Hostname: "192.0.2.11", SSHPort: 22, SSHUser: "root", Fingerprint: "test"}, a.box.Seal([]byte("fake"), "server:"+other))
	if err != nil {
		t.Fatal(err)
	}
	target, err = a.db.Target(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	if target.EgressMode != "direct" || len(target.EncryptedWarp) > 0 {
		t.Fatal("egress state crossed servers")
	}
	requireStatus(t, request(a, "POST", "/api/admin/servers/"+other+"/egress", admin, `{"mode":"warp","accept_terms":true}`), 409)
}

func TestEgressUpgradeDefaultsToDirectWithoutChangingProfile(t *testing.T) {
	a, admin, id, w, _ := vpnSetup(t)
	ctx := context.Background()
	d := deviceFor(t, a, admin, "Test phone")
	p := issueFor(t, a, admin, d)
	applyJob(t, a, w, "")
	before := request(a, "GET", "/api/profiles/"+p.ID+"/download", admin, "").Body.String()
	if _, err := a.db.Pool.Exec(ctx, `ALTER TABLE servers DROP COLUMN egress_mode, DROP COLUMN applied_egress_mode, DROP COLUMN encrypted_warp_config; DELETE FROM schema_migrations WHERE name='014_server_egress.sql'`); err != nil {
		t.Fatal(err)
	}
	upgraded, err := store.Open(ctx, a.db.Pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	upgraded.Pool.Close()
	target, err := a.db.Target(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if target.EgressMode != "direct" || target.AppliedEgressMode != "direct" || len(target.EncryptedWarp) > 0 {
		t.Fatal("upgrade activated WARP")
	}
	after := request(a, "GET", "/api/profiles/"+p.ID+"/download", admin, "")
	requireStatus(t, after, 200)
	if after.Body.String() != before {
		t.Fatal("migration changed profile")
	}
}
