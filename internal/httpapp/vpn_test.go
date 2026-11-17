package httpapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/NetherQuartz/cu-chi/internal/awg"
	"github.com/NetherQuartz/cu-chi/internal/cryptobox"
	"github.com/NetherQuartz/cu-chi/internal/deployer"
	"github.com/NetherQuartz/cu-chi/internal/store"
	"github.com/NetherQuartz/cu-chi/internal/worker"
)

type fakeVPN struct {
	payloads []deployer.Payload
	fail     bool
}

func (f *fakeVPN) Check(context.Context, deployer.Credentials) error { return nil }
func (f *fakeVPN) Apply(_ context.Context, _ deployer.Credentials, p deployer.Payload) error {
	f.payloads = append(f.payloads, p)
	if f.fail {
		return errors.New("test VPS offline")
	}
	return nil
}
func vpnSetup(t *testing.T) (*App, identity, string, *worker.Worker, *fakeVPN) {
	t.Helper()
	a := testApp(t)
	admin := account(t, a, "admin")
	a.cfg.AWGImage = "ghcr.io/netherquartz/cu-chi-awg@sha256:" + strings.Repeat("a", 64)
	id := cryptobox.ID()
	_, err := a.db.CreateServer(context.Background(), admin.user, store.Server{ID: id, Name: "Test VPS", Hostname: "192.0.2.10", SSHPort: 22, SSHUser: "root", Fingerprint: "test"}, a.box.Seal([]byte("fake transport only"), "server:"+id))
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeVPN{}
	w := &worker.Worker{DB: a.db, Box: a.box, Remote: f, Image: a.cfg.AWGImage}
	requireStatus(t, request(a, "POST", "/api/admin/servers/"+id+"/deploy", admin, `{"endpoint":"vpn.example","port":51820}`), 202)
	applyJob(t, a, w, "")
	requireStatus(t, request(a, "POST", "/api/admin/servers/"+id+"/default", admin, ""), 204)
	return a, admin, id, w, f
}
func applyJob(t *testing.T, a *App, w *worker.Worker, wantError string) (store.Job, store.Target) {
	t.Helper()
	ctx := context.Background()
	j, err := a.db.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target, err := a.db.Target(ctx, j.ServerID)
	if err != nil {
		t.Fatal(err)
	}
	err = w.Execute(ctx, j, target)
	failure := ""
	if err != nil {
		failure = err.Error()
	}
	if failure != wantError {
		t.Fatalf("worker result %q, want %q", failure, wantError)
	}
	if err = a.db.Finish(ctx, j, target, failure); err != nil {
		t.Fatal(err)
	}
	return j, target
}
func deviceFor(t *testing.T, a *App, i identity, name string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"name": name, "platform": "ios"})
	r := request(a, "POST", "/api/devices", i, string(body))
	requireStatus(t, r, 201)
	var d store.Device
	if err := json.Unmarshal(r.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	return d.ID
}
func issueFor(t *testing.T, a *App, i identity, d string) store.Profile {
	t.Helper()
	r := request(a, "POST", "/api/devices/"+d+"/profiles", i, "")
	requireStatus(t, r, 202)
	var p store.Profile
	if err := json.Unmarshal(r.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestVPNLifecycleOwnershipAndRetry(t *testing.T) {
	a, admin, server, w, remote := vpnSetup(t)
	ctx := context.Background()
	alice, bob := account(t, a, "user"), account(t, a, "user")
	d := deviceFor(t, a, alice, "Phone")
	p := issueFor(t, a, alice, d)
	requireStatus(t, request(a, "POST", "/api/devices/"+d+"/profiles", alice, ""), 409)
	requireStatus(t, request(a, "POST", "/api/devices/"+d+"/profiles", bob, ""), 404)
	requireStatus(t, request(a, "POST", "/api/devices/"+d+"/revoke", bob, ""), 404)
	requireStatus(t, request(a, "POST", "/api/admin/servers/"+server+"/default", alice, ""), 403)
	requireStatus(t, request(a, "DELETE", "/api/devices/"+d, alice, ""), 409)
	requireStatus(t, request(a, "GET", "/api/profiles/"+p.ID+"/download", alice, ""), 404)
	remote.fail = true
	oldJob, oldTarget := applyJob(t, a, w, "test VPS offline")
	firstPayload := remote.payloads[len(remote.payloads)-1]
	_, err := a.db.Pool.Exec(ctx, "UPDATE jobs SET run_after=now() WHERE id=$1", oldJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	remote.fail = false
	newJob, err := a.db.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if newJob.RunToken == oldJob.RunToken {
		t.Fatal("job attempt token reused")
	}
	if err = a.db.Finish(ctx, oldJob, oldTarget, ""); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, request(a, "GET", "/api/profiles/"+p.ID+"/download", alice, ""), 404)
	target, err := a.db.Target(ctx, server)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Execute(ctx, newJob, target); err != nil {
		t.Fatal(err)
	}
	if firstPayload.Config != remote.payloads[len(remote.payloads)-1].Config {
		t.Fatal("retry changed keys")
	}
	if err = a.db.Finish(ctx, newJob, target, ""); err != nil {
		t.Fatal(err)
	}
	download := request(a, "GET", "/api/profiles/"+p.ID+"/download", alice, "")
	requireStatus(t, download, 200)
	if !strings.Contains(download.Body.String(), "HeaderProtectionKey = ") || !strings.Contains(download.Body.String(), "RandomTrailers = 1") {
		t.Fatal("missing AWG 3.1 settings")
	}
	if download.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("secret response cacheable")
	}
	again := request(a, "GET", "/api/profiles/"+p.ID+"/download", alice, "")
	if again.Body.String() != download.Body.String() {
		t.Fatal("download rotated keys")
	}
	requireStatus(t, request(a, "GET", "/api/profiles/"+p.ID+"/download", bob, ""), 404)
	qr := request(a, "GET", "/api/profiles/"+p.ID+"/qr", alice, "")
	requireStatus(t, qr, 200)
	if !bytes.HasPrefix(qr.Body.Bytes(), []byte{137, 80, 78, 71}) {
		t.Fatal("invalid QR PNG")
	}
	requireStatus(t, request(a, "GET", "/api/profiles/"+p.ID+"/qr", bob, ""), 404)
	list := request(a, "GET", "/api/profiles", bob, "")
	if strings.TrimSpace(list.Body.String()) != "[]" {
		t.Fatal("profiles leaked")
	}
	profile, target, err := a.db.Download(ctx, alice.user, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := a.box.Open(profile.EncryptedKeys, "profile:"+p.ID)
	if err != nil {
		t.Fatal(err)
	}
	var keys awg.Keys
	if err = json.Unmarshal(plain, &keys); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(profile.EncryptedKeys, []byte(keys.Private)) {
		t.Fatal("key not encrypted")
	}
	for _, path := range []string{"/api/profiles", "/api/admin/events", "/api/admin/jobs"} {
		response := request(a, "GET", path, admin, "")
		if strings.Contains(response.Body.String(), keys.Private) || strings.Contains(response.Body.String(), keys.PSK) {
			t.Fatal("key leaked")
		}
	}
	d2 := deviceFor(t, a, alice, "Tablet")
	p2 := issueFor(t, a, alice, d2)
	applyJob(t, a, w, "")
	if p2.PublicKey == p.PublicKey || p2.Address == p.Address {
		t.Fatal("devices share identity")
	}
	replacement := issueFor(t, a, alice, d)
	if replacement.Revision != 2 || replacement.PublicKey == p.PublicKey {
		t.Fatal("reissue did not rotate")
	}
	requireStatus(t, request(a, "GET", "/api/profiles/"+p.ID+"/download", alice, ""), 404)
	applyJob(t, a, w, "")
	last := remote.payloads[len(remote.payloads)-1].Config
	if strings.Contains(last, p.PublicKey) || !strings.Contains(last, replacement.PublicKey) {
		t.Fatal("reissue did not reconcile peers")
	}
	requireStatus(t, request(a, "POST", "/api/devices/"+d+"/revoke", alice, ""), 202)
	remote.fail = true
	job, _ := applyJob(t, a, w, "test VPS offline")
	requireStatus(t, request(a, "DELETE", "/api/devices/"+d, alice, ""), 409)
	requireStatus(t, request(a, "GET", "/api/profiles/"+replacement.ID+"/download", alice, ""), 404)
	remote.fail = false
	if _, err = a.db.Pool.Exec(ctx, "UPDATE jobs SET run_after=now() WHERE id=$1", job.ID); err != nil {
		t.Fatal(err)
	}
	applyJob(t, a, w, "")
	last = remote.payloads[len(remote.payloads)-1].Config
	if strings.Contains(last, replacement.PublicKey) || !strings.Contains(last, p2.PublicKey) {
		t.Fatal("revocation affected wrong peer")
	}
	requireStatus(t, request(a, "DELETE", "/api/devices/"+d, alice, ""), 204)
}
func TestVPNConcurrentRevocationDoesNotBecomeReady(t *testing.T) {
	a, _, _, w, _ := vpnSetup(t)
	u := account(t, a, "user")
	d := deviceFor(t, a, u, "Phone")
	p := issueFor(t, a, u, d)
	ctx := context.Background()
	j, err := a.db.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := a.db.Target(ctx, j.ServerID)
	if err != nil {
		t.Fatal(err)
	}
	requireStatus(t, request(a, "POST", "/api/devices/"+d+"/revoke", u, ""), 202)
	if err = w.Execute(ctx, j, snapshot); err != nil {
		t.Fatal(err)
	}
	if err = a.db.Finish(ctx, j, snapshot, ""); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, request(a, "GET", "/api/profiles/"+p.ID+"/download", u, ""), 404)
	profiles, err := a.db.Profiles(ctx, u.user)
	if err != nil || len(profiles) != 1 || profiles[0].State != "revoking" {
		t.Fatalf("new intent lost: %+v %v", profiles, err)
	}
	applyJob(t, a, w, "")
	requireStatus(t, request(a, "DELETE", "/api/devices/"+d, u, ""), 204)
}

func TestVPNServerMigrationKeepsOfflineRevocationVisible(t *testing.T) {
	a, admin, first, w, _ := vpnSetup(t)
	u := account(t, a, "user")
	d := deviceFor(t, a, u, "Phone")
	old := issueFor(t, a, u, d)
	applyJob(t, a, w, "")
	ctx := context.Background()
	second := cryptobox.ID()
	_, err := a.db.CreateServer(ctx, admin.user, store.Server{ID: second, Name: "Replacement", Hostname: "192.0.2.20", SSHPort: 22, SSHUser: "root", Fingerprint: "test"}, a.box.Seal([]byte("fake"), "server:"+second))
	if err != nil {
		t.Fatal(err)
	}
	requireStatus(t, request(a, "POST", "/api/admin/servers/"+second+"/default", admin, ""), 409)
	requireStatus(t, request(a, "POST", "/api/admin/servers/"+second+"/deploy", admin, `{"endpoint":"new.example","port":51820}`), 202)
	applyJob(t, a, w, "")
	requireStatus(t, request(a, "POST", "/api/admin/servers/"+second+"/default", admin, ""), 204)
	// Selecting a new default alone must not change an existing profile.
	requireStatus(t, request(a, "GET", "/api/profiles/"+old.ID+"/download", u, ""), 200)
	next := issueFor(t, a, u, d)
	if next.ServerID != second {
		t.Fatal("new default not used")
	}
	for range 2 {
		j, err := a.db.Claim(ctx)
		if err != nil {
			t.Fatal(err)
		}
		target, err := a.db.Target(ctx, j.ServerID)
		if err != nil {
			t.Fatal(err)
		}
		failure := ""
		if j.ServerID == first {
			failure = "test old VPS offline"
		} else if err = w.Execute(ctx, j, target); err != nil {
			t.Fatal(err)
		}
		if err = a.db.Finish(ctx, j, target, failure); err != nil {
			t.Fatal(err)
		}
	}
	requireStatus(t, request(a, "GET", "/api/profiles/"+next.ID+"/download", u, ""), 200)
	profiles, err := a.db.Profiles(ctx, u.user)
	if err != nil || len(profiles) != 2 {
		t.Fatalf("lost old pending revocation: %v %v", profiles, err)
	}
	for _, p := range profiles {
		if p.ID == old.ID && (p.State != "revoking" || p.Desired) {
			t.Fatal("offline revocation falsely completed")
		}
	}
}

func TestVPNWorkerRecoversInterruptedJob(t *testing.T) {
	a, _, _, w, _ := vpnSetup(t)
	u := account(t, a, "user")
	d := deviceFor(t, a, u, "Phone")
	p := issueFor(t, a, u, d)
	ctx := context.Background()
	if _, err := a.db.Claim(ctx); err != nil {
		t.Fatal(err)
	} // simulate a crash after claiming
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); w.Run(runCtx) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, _, err := a.db.Download(ctx, u.user, p.ID)
		if err == nil {
			return
		}
		if !errors.Is(err, store.ErrNotFound) {
			t.Fatal(err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("interrupted job was not recovered")
}
