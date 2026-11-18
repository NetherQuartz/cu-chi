package httpapp

import (
	"context"
	"strings"
	"testing"

	"github.com/NetherQuartz/cu-chi/internal/cryptobox"
	"github.com/NetherQuartz/cu-chi/internal/store"
)

func TestMigrationCleanupDoesNotAlertOrBlockNewProfiles(t *testing.T) {
	a, admin, oldServer, w, _ := vpnSetup(t)
	ctx := context.Background()
	alice, bob := account(t, a, "user"), account(t, a, "user")
	da, db := deviceFor(t, a, alice, "Phone"), deviceFor(t, a, bob, "Laptop")
	oldA := issueFor(t, a, alice, da)
	issueFor(t, a, bob, db)
	applyJob(t, a, w, "")
	replacement := cryptobox.ID()
	_, err := a.db.CreateServer(ctx, admin.user, store.Server{ID: replacement, Name: "Replacement", Hostname: "192.0.2.20", SSHPort: 22, SSHUser: "root", Fingerprint: "test"}, a.box.Seal([]byte("fake"), "server:"+replacement))
	if err != nil {
		t.Fatal(err)
	}
	requireStatus(t, request(a, "POST", "/api/admin/servers/"+replacement+"/deploy", admin, `{"endpoint":"new.example","port":51820}`), 202)
	applyJob(t, a, w, "")
	requireStatus(t, request(a, "POST", "/api/admin/servers/"+replacement+"/default", admin, ""), 204)
	nextA := issueFor(t, a, alice, da)
	issueFor(t, a, bob, db)
	requireStatus(t, request(a, "POST", "/api/push/subscription", admin, pushBody("https://fcm.googleapis.com/fcm/send/admin")), 204)
	// Exhaust each old-server job, apply the replacement independently.
	for i := 0; i < 4; i++ {
		j, e := a.db.Claim(ctx)
		if e != nil {
			t.Fatal(e)
		}
		target, e := a.db.Target(ctx, j.ServerID)
		if e != nil {
			t.Fatal(e)
		}
		failure := ""
		if j.ServerID == oldServer {
			failure = "SSH connection failed"
		}
		if e = a.db.Finish(ctx, j, target, failure); e != nil {
			t.Fatal(e)
		}
		if _, e = a.db.Pool.Exec(ctx, `UPDATE jobs SET run_after=now() WHERE status='queued'`); e != nil {
			t.Fatal(e)
		}
	}
	requireStatus(t, request(a, "GET", "/api/profiles/"+nextA.ID+"/download", alice, ""), 200)
	requireStatus(t, request(a, "GET", "/api/profiles/"+oldA.ID+"/download", alice, ""), 404)
	requireStatus(t, request(a, "DELETE", "/api/devices/"+da, alice, ""), 409)
	profiles, err := a.db.Profiles(ctx, alice.user)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range profiles {
		if p.NeedsAttention {
			t.Fatal("cleanup marked current profile broken")
		}
		if p.ID == oldA.ID && p.State != "revoking" {
			t.Fatal("revocation falsely confirmed")
		}
	}
	servers, err := a.db.Servers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range servers {
		if s.MigrationCleanup != (s.ID == oldServer) {
			t.Fatal("wrong cleanup classification", s.ID)
		}
	}
	notices, err := a.db.Inbox(ctx, admin.user)
	if err != nil || len(notices) != 1 {
		t.Fatal("cleanup history missing", notices, err)
	}
	n := notices[0]
	if n.Operation != "migration_cleanup" || n.Severity != "info" || !strings.Contains(n.ActorNames, alice.user.Username) || !strings.Contains(n.ActorNames, bob.user.Username) {
		t.Fatal("lost merged actors/cleanup context", n)
	}
	if err = a.db.QueuePush(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = a.db.Pool.QueryRow(ctx, `SELECT count(*) FROM push_outbox`).Scan(&count); err != nil || count != 0 {
		t.Fatal("cleanup generated push", count, err)
	}
	// Exercise the actual upgrade with existing profiles/jobs and legacy notices.
	// Only this disposable test schema is restored to its pre-013 shape.
	before := request(a, "GET", "/api/profiles/"+nextA.ID+"/download", alice, "").Body.String()
	if _, err = a.db.Pool.Exec(ctx, `DROP TABLE job_actors; ALTER TABLE notifications DROP COLUMN actor_names, DROP COLUMN operation; DELETE FROM schema_migrations WHERE name='013_operation_context.sql'`); err != nil {
		t.Fatal(err)
	}
	upgraded, err := store.Open(ctx, a.db.Pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	upgraded.Pool.Close()
	legacy, err := a.db.Inbox(ctx, admin.user)
	if err != nil || len(legacy) != 1 || legacy[0].Operation != "migration_cleanup" || legacy[0].ActorNames != alice.user.Username {
		t.Fatal("upgrade lost legacy context", legacy, err)
	}
	after := request(a, "GET", "/api/profiles/"+nextA.ID+"/download", alice, "").Body.String()
	if before != after {
		t.Fatal("upgrade changed the ready profile")
	}
	if err = a.db.QueuePush(ctx); err != nil {
		t.Fatal(err)
	}
	if err = a.db.Pool.QueryRow(ctx, `SELECT count(*) FROM push_outbox`).Scan(&count); err != nil || count != 0 {
		t.Fatal("legacy cleanup pushed after upgrade", count, err)
	}
	// An explicit revocation without a replacement must still require attention.
	requireStatus(t, request(a, "POST", "/api/devices/"+da+"/revoke", alice, ""), 202)
	// Exhaust every queued removal. At least the old server must generate a warning now.
	for i := 0; i < 6; i++ {
		j, e := a.db.Claim(ctx)
		if e != nil {
			t.Fatal(e)
		}
		target, e := a.db.Target(ctx, j.ServerID)
		if e != nil {
			t.Fatal(e)
		}
		if e = a.db.Finish(ctx, j, target, "test unavailable"); e != nil {
			t.Fatal(e)
		}
		if _, e = a.db.Pool.Exec(ctx, `UPDATE jobs SET run_after=now() WHERE status='queued'`); e != nil {
			t.Fatal(e)
		}
	}
	notices, err = a.db.Inbox(ctx, admin.user)
	if err != nil {
		t.Fatal(err)
	}
	warning := false
	for _, n := range notices {
		if n.ServerName == "Test VPS" && n.Severity == "warning" && n.Operation == "sync" {
			warning = true
		}
	}
	if !warning {
		t.Fatal("explicit revocation was silenced")
	}
	if err = a.db.QueuePush(ctx); err != nil {
		t.Fatal(err)
	}
	push, err := a.db.ClaimPush(ctx)
	if err != nil || !push.Eligible || push.ActorNames != alice.user.Username || push.ServerName == "" {
		t.Fatal("operation push context", push, err)
	}
	// Another user cannot read administrator operation identities.
	ordinary, err := a.db.Inbox(ctx, bob.user)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range ordinary {
		if n.Kind == "job_failed" || n.ActorNames != "" {
			t.Fatal("admin context leaked")
		}
	}
}

func TestPendingProfileFailureStillAlertsMergedActors(t *testing.T) {
	a, admin, _, _, _ := vpnSetup(t)
	ctx := context.Background()
	alice, bob := account(t, a, "user"), account(t, a, "user")
	issueFor(t, a, alice, deviceFor(t, a, alice, "Phone"))
	issueFor(t, a, bob, deviceFor(t, a, bob, "Laptop"))
	for i := 0; i < 3; i++ {
		j, e := a.db.Claim(ctx)
		if e != nil {
			t.Fatal(e)
		}
		target, e := a.db.Target(ctx, j.ServerID)
		if e != nil {
			t.Fatal(e)
		}
		if e = a.db.Finish(ctx, j, target, "test installation failed"); e != nil {
			t.Fatal(e)
		}
		if _, e = a.db.Pool.Exec(ctx, `UPDATE jobs SET run_after=now() WHERE status='queued'`); e != nil {
			t.Fatal(e)
		}
	}
	ns, e := a.db.Inbox(ctx, admin.user)
	if e != nil || len(ns) != 1 {
		t.Fatal(ns, e)
	}
	if ns[0].Severity != "warning" || ns[0].Operation != "sync" || !strings.Contains(ns[0].ActorNames, alice.user.Username) || !strings.Contains(ns[0].ActorNames, bob.user.Username) {
		t.Fatal(ns)
	}
	ps, e := a.db.Profiles(ctx, alice.user)
	if e != nil || len(ps) != 1 || !ps[0].NeedsAttention {
		t.Fatal("real profile failure hidden", ps, e)
	}
}
