package httpapp

import (
	"context"
	"encoding/json"
	"github.com/NetherQuartz/cu-chi/internal/store"
	"testing"
	"time"
)

func TestHealthDebounceAudienceRecoveryAndMaintenance(t *testing.T) {
	a, admin, id, w, _ := vpnSetup(t)
	ctx := context.Background()
	user, other := account(t, a, "user"), account(t, a, "user")
	d := deviceFor(t, a, user, "Phone")
	issueFor(t, a, user, d)
	applyJob(t, a, w, "")
	target, err := a.db.Target(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(-time.Hour)
	save := func(result string) {
		t.Helper()
		at = at.Add(5 * time.Minute)
		if e := a.db.SaveProbe(ctx, id, target.Generation, at, result); e != nil {
			t.Fatal(e)
		}
	}
	inbox := func(i identity) []store.Notification {
		t.Helper()
		r := request(a, "GET", "/api/notifications", i, "")
		requireStatus(t, r, 200)
		var n []store.Notification
		if e := json.Unmarshal(r.Body.Bytes(), &n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	save("management")
	save("management")
	if len(inbox(admin)) != 0 {
		t.Fatal("alert too early")
	}
	save("management")
	if len(inbox(admin)) != 1 || len(inbox(user)) != 0 {
		t.Fatal("management audience")
	}
	save("data")
	save("data")
	save("data")
	if len(inbox(admin)) != 2 || len(inbox(user)) != 1 || len(inbox(other)) != 0 {
		t.Fatal("data audience")
	}
	// A retry cannot increment failures or create duplicate notices.
	if err = a.db.SaveProbe(ctx, id, target.Generation, at, "data"); err != nil {
		t.Fatal(err)
	}
	save("data")
	if len(inbox(user)) != 1 {
		t.Fatal("repeat notice")
	}
	save("ok")
	if len(inbox(user)) != 1 {
		t.Fatal("early recovery")
	}
	save("ok")
	notices := inbox(user)
	if len(notices) != 2 {
		t.Fatal("recovery missing")
	}
	requireStatus(t, request(a, "POST", "/api/notifications/"+notices[0].ID+"/read", other, ""), 404)
	requireStatus(t, request(a, "POST", "/api/notifications/"+notices[0].ID+"/read", user, ""), 204)
	path := "/api/admin/servers/" + id + "/maintenance"
	requireStatus(t, request(a, "POST", path, user, `{"minutes":60}`), 403)
	requireStatus(t, request(a, "POST", path, admin, `{"minutes":60}`), 204)
	save("data")
	save("data")
	save("data")
	if len(inbox(user)) != 2 {
		t.Fatal("maintenance alert")
	}
	requireStatus(t, request(a, "GET", "/api/health", user, ""), 200)
	requireStatus(t, request(a, "GET", "/api/admin/reminders", user, ""), 403)
}

func TestOutagePushPreferencesAndRecoveryEligibility(t *testing.T) {
	a, _, id, w, _ := vpnSetup(t)
	ctx := context.Background()
	user := account(t, a, "user")
	d := deviceFor(t, a, user, "Phone")
	issueFor(t, a, user, d)
	applyJob(t, a, w, "")
	requireStatus(t, request(a, "POST", "/api/push/subscription", user, pushBody("https://fcm.googleapis.com/fcm/send/phone")), 204)
	target, err := a.db.Target(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour)
	save := func(result string) {
		t.Helper()
		at = at.Add(5 * time.Minute)
		if e := a.db.SaveProbe(ctx, id, target.Generation, at, result); e != nil {
			t.Fatal(e)
		}
	}
	save("data")
	save("data")
	save("data")
	if err = a.db.QueuePush(ctx); err != nil {
		t.Fatal(err)
	}
	var pending int
	if err = a.db.Pool.QueryRow(ctx, `SELECT count(*) FROM push_outbox`).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("stale outage was queued", err)
	}
	// After the monitor returns, only fresh incident evidence may authorize a push.
	if _, err = a.db.Pool.Exec(ctx, `UPDATE health_state SET attempted_at=now() WHERE server_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err = a.db.QueuePush(ctx); err != nil {
		t.Fatal(err)
	}
	// Disable category after queueing, before the sender claims it.
	prefs := store.PushPreferences{Outages: false, Recovery: true, Reminders: true, Operations: true}
	if err = a.db.SavePushPreferences(ctx, user.user, prefs); err != nil {
		t.Fatal(err)
	}
	job, err := a.db.ClaimPush(ctx)
	if err != nil || job.Eligible {
		t.Fatal("preference change not rechecked", err)
	}
	if err = a.db.FinishPush(ctx, job, 0, true); err != nil {
		t.Fatal(err)
	}
	at = time.Now()
	save("ok")
	save("ok")
	if err = a.db.QueuePush(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = a.db.Pool.QueryRow(ctx, `SELECT count(*) FROM push_outbox o JOIN notifications n ON n.id=o.notification_id WHERE n.kind='recovery'`).Scan(&n); err != nil || n != 0 {
		t.Fatal("recovery pushed without an accepted outage", err)
	}
}
