package httpapp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/NetherQuartz/cu-chi/internal/cryptobox"
	"github.com/NetherQuartz/cu-chi/internal/store"
)

func TestServerLifecycle(t *testing.T) {
	a, admin, id, w, _ := vpnSetup(t)
	ctx := context.Background()
	owner := account(t, a, "user")
	d := deviceFor(t, a, owner, "Phone")
	p := issueFor(t, a, owner, d)
	applyJob(t, a, w, "")
	path := "/api/admin/servers/" + id
	edit := map[string]any{"name": "West", "hostname": "192.0.2.20", "ssh_port": 2222, "ssh_user": "operator", "ssh_auth_method": "password", "endpoint": "new.example", "edit_revision": 1}
	put := func(actor identity, status int) {
		t.Helper()
		b, err := json.Marshal(edit)
		if err != nil {
			t.Fatal(err)
		}
		requireStatus(t, request(a, "PUT", path, actor, string(b)), status)
	}
	put(owner, 403)
	put(admin, 400)
	edit["password"] = "fixture-password"
	put(admin, 204)
	put(admin, 409)
	delete(edit, "password")
	edit["edit_revision"] = 2
	put(admin, 204)
	target, err := a.db.Target(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := a.box.Open(target.EncryptedKey, "server:"+id)
	if err != nil || string(plain) != "fixture-password" || target.Hostname != "192.0.2.20" || target.SSHUser != "operator" || target.SSHPort != 2222 || target.Fingerprint != "test" {
		t.Fatal("SSH address, credential or pin not preserved")
	}
	profile := request(a, "GET", "/api/profiles/"+p.ID+"/download", owner, "")
	requireStatus(t, profile, 200)
	if !strings.Contains(profile.Body.String(), "new.example:51820") {
		t.Fatal("download has stale endpoint")
	}
	keyResp := request(a, "POST", "/api/admin/ssh-keys", admin, `{"name":"Test key","mode":"generate"}`)
	requireStatus(t, keyResp, 201)
	var key store.SSHKey
	if err = json.Unmarshal(keyResp.Body.Bytes(), &key); err != nil {
		t.Fatal(err)
	}
	edit["ssh_auth_method"] = "saved_key"
	edit["ssh_key_id"] = key.ID
	edit["edit_revision"] = 3
	put(admin, 204)
	target, err = a.db.Target(ctx, id)
	if err != nil || target.SSHKeyID != key.ID || target.AuthMethod != "saved_key" {
		t.Fatal("saved key not selected")
	}
	requireStatus(t, request(a, "POST", path+"/check", admin, ""), 202)
	requireStatus(t, request(a, "DELETE", path, owner, ""), 403)

	// Snapshot an actually applied Xray grant, as well as the ordinary AWG profile.
	if err = a.db.XDeploy(ctx, admin.user, id, []byte("fixture"), "public", "short", true); err != nil {
		t.Fatal(err)
	}
	work, err := a.db.XNext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.db.XFinish(ctx, work, "example.org", ""); err != nil {
		t.Fatal(err)
	}
	if err = a.db.XChange(ctx, owner.user, d, true, []byte("fixture"), "test"); err != nil {
		t.Fatal(err)
	}
	work, err = a.db.XNext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.db.XFinish(ctx, work, "example.org", ""); err != nil {
		t.Fatal(err)
	}

	if _, err = a.db.Pool.Exec(ctx, `INSERT INTO traffic_totals(device_id,device_name,server_id,user_id,first_at,last_at,upload) VALUES($1,'Phone',$2,$3,now(),now(),321)`, d, id, owner.user.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err := a.db.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	guard, err := a.db.BeginServerUse(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	busy := request(a, "DELETE", path, admin, "")
	guard.Rollback(ctx)
	requireStatus(t, busy, 409)
	requireStatus(t, request(a, "DELETE", path, admin, ""), 204)
	requireStatus(t, request(a, "DELETE", path, admin, ""), 404)
	for _, suffix := range []string{"/check", "/default", "/xray"} {
		requireStatus(t, request(a, "POST", path+suffix, admin, ""), 404)
	}
	requireStatus(t, request(a, "GET", "/api/profiles/"+p.ID+"/download", owner, ""), 404)
	put(admin, 404)
	if err = a.db.Finish(ctx, claimed, target, "late result"); err != nil {
		t.Fatal(err)
	}
	jobs, err := a.db.Jobs(ctx)
	if err != nil || jobs[0].Status != "cancelled" {
		t.Fatal("cancelled job was revived")
	}
	traffic, err := a.db.Traffic(ctx, owner.user, store.TrafficFilter{ServerID: id, Since: time.Now().Add(-time.Hour), Bucket: 60})
	if err != nil || len(traffic.Rows) != 1 || traffic.Rows[0].ServerName != "West (удалён)" {
		t.Fatalf("archived traffic missing: %v", err)
	}
	other := account(t, a, "user")
	if got := request(a, "GET", "/api/xray", other, ""); strings.Contains(got.Body.String(), "West") {
		t.Fatal("another account sees archived Xray device")
	}

	for _, route := range []string{"/api/profiles", "/api/xray", "/api/admin/events", "/api/admin/jobs", "/api/admin/servers", "/api/notifications"} {
		actor := admin
		if route == "/api/profiles" || route == "/api/xray" {
			actor = owner
		}
		r := request(a, "GET", route, actor, "")
		requireStatus(t, r, 200)
		if !strings.Contains(r.Body.String(), "West (удалён)") {
			t.Fatalf("missing archive label in %s", route)
		}
	}
	var cleared bool
	if err = a.db.Pool.QueryRow(ctx, "SELECT ssh_key_id IS NULL AND encrypted_private_key IS NULL AND encrypted_vpn_settings IS NULL FROM servers WHERE id=$1", id).Scan(&cleared); err != nil || !cleared {
		t.Fatal("server credentials not cleared")
	}
	requireStatus(t, request(a, "DELETE", "/api/admin/ssh-keys/"+key.ID, admin, ""), 204)
	// Reuse the address and reissue without scheduling SSH to the retired VPS.
	replacement := cryptobox.ID()
	_, err = a.db.CreateServer(ctx, admin.user, store.Server{ID: replacement, Name: "Replacement", Hostname: target.Hostname, SSHPort: 2222, SSHUser: "root", Fingerprint: "test"}, a.box.Seal([]byte("fixture"), "server:"+replacement))
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/admin/servers/" + replacement
	requireStatus(t, request(a, "POST", base+"/deploy", admin, `{"endpoint":"next.example","port":51820}`), 202)
	applyJob(t, a, w, "")
	requireStatus(t, request(a, "POST", base+"/default", admin, ""), 204)
	next := issueFor(t, a, owner, d)
	if next.Revision != p.Revision+1 || next.ServerID != replacement {
		t.Fatal("incorrect replacement profile")
	}
	applyJob(t, a, w, "")
	var waiting int
	if err = a.db.Pool.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE server_id=$1 AND status IN ('queued','running')", id).Scan(&waiting); err != nil || waiting != 0 {
		t.Fatal("work scheduled for retired server")
	}
}
