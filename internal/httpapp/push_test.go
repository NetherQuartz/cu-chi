package httpapp

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/NetherQuartz/cu-chi/internal/cryptobox"
	"github.com/NetherQuartz/cu-chi/internal/store"
	"github.com/jackc/pgx/v5"
	"strings"
	"testing"
)

func pushBody(endpoint string) string {
	k, _ := ecdh.P256().GenerateKey(rand.Reader)
	b, _ := json.Marshal(map[string]any{"endpoint": endpoint, "expirationTime": nil, "keys": map[string]string{"p256dh": base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()), "auth": base64.RawURLEncoding.EncodeToString(make([]byte, 16))}})
	return string(b)
}
func TestPushOwnershipPreferencesDeliveryAndExpiry(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()
	alice, bob := account(t, a, "user"), account(t, a, "user")
	requireStatus(t, request(a, "GET", "/api/push", alice, ""), 200)
	endpoint := "https://fcm.googleapis.com/fcm/send/secret-capability"
	body := pushBody(endpoint)
	requireStatus(t, request(a, "POST", "/api/push/subscription", alice, body), 204)
	requireStatus(t, request(a, "POST", "/api/push/subscription", bob, body), 409)
	requireStatus(t, request(a, "POST", "/api/push/subscription", alice, pushBody("https://127.0.0.1/a")), 400)
	bad := alice
	bad.csrf = ""
	requireStatus(t, request(a, "POST", "/api/push/subscription", bad, body), 403)
	var id string
	var encrypted []byte
	if err := a.db.Pool.QueryRow(ctx, `SELECT id::text,encrypted_subscription FROM push_subscriptions`).Scan(&id, &encrypted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encrypted), "secret-capability") {
		t.Fatal("subscription plaintext")
	}
	if _, err := a.box.Open(encrypted, "push-subscription:"+id); err != nil {
		t.Fatal(err)
	}
	if _, err := a.box.Open(encrypted, "push-subscription:"+cryptobox.ID()); err == nil {
		t.Fatal("AAD not bound")
	}
	requireStatus(t, request(a, "POST", "/api/push/test", alice, `{"endpoint":"`+endpoint+`"}`), 202)
	if err := a.db.QueuePush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueuePush(ctx); err != nil {
		t.Fatal(err)
	}
	job, err := a.db.ClaimPush(ctx)
	if err != nil || !job.Eligible {
		t.Fatal("claim", job, err)
	}
	if _, err = a.db.ClaimPush(ctx); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("duplicate claim", err)
	}
	if err = a.db.FinishPush(ctx, job, 503, false); err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.ClaimPush(ctx); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("backoff ignored")
	}
	if _, err = a.db.Pool.Exec(ctx, `UPDATE push_outbox SET next_at=now()`); err != nil {
		t.Fatal(err)
	}
	retried, err := a.db.ClaimPush(ctx)
	if err != nil || retried.Attempts != 2 || retried.Lease == job.Lease {
		t.Fatal("retry", err)
	}
	// A stale attempt cannot remove the live subscription or complete a newer lease.
	if err = a.db.FinishPush(ctx, job, 410, false); err != nil {
		t.Fatal(err)
	}
	if err = a.db.FinishPush(ctx, retried, 201, false); err != nil {
		t.Fatal(err)
	}
	if err = a.db.QueuePush(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.ClaimPush(ctx); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("accepted sent twice")
	}
	if _, err = a.db.Pool.Exec(ctx, `INSERT INTO notifications(user_id,event_key,kind,severity) VALUES($1,'expired-test','test','info')`, alice.user.ID); err != nil {
		t.Fatal(err)
	}
	if err = a.db.QueuePush(ctx); err != nil {
		t.Fatal(err)
	}
	job, err = a.db.ClaimPush(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.db.FinishPush(ctx, job, 410, false); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = a.db.Pool.QueryRow(ctx, `SELECT count(*) FROM push_subscriptions`).Scan(&count); err != nil || count != 0 {
		t.Fatal("expired subscription retained")
	}
	requireStatus(t, request(a, "POST", "/api/push/subscription", alice, body), 204)
	if _, err = a.db.Pool.Exec(ctx, `INSERT INTO notifications(user_id,event_key,kind,severity) VALUES($1,'deny-test','test','info')`, alice.user.ID); err != nil {
		t.Fatal(err)
	}
	if err = a.db.QueuePush(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.Pool.Exec(ctx, `UPDATE users SET role='denied' WHERE id=$1`, alice.user.ID); err != nil {
		t.Fatal(err)
	}
	job, err = a.db.ClaimPush(ctx)
	if err != nil || job.Eligible {
		t.Fatal("denied user eligible", err)
	}
}
func TestReminderDeduplicationAndReissueSuppression(t *testing.T) {
	a, admin, old, w, _ := vpnSetup(t)
	ctx := context.Background()
	u := account(t, a, "user")
	d := deviceFor(t, a, u, "Phone")
	issueFor(t, a, u, d)
	applyJob(t, a, w, "")
	requireStatus(t, request(a, "POST", "/api/push/subscription", u, pushBody("https://fcm.googleapis.com/fcm/send/a")), 204)
	next := cryptobox.ID()
	_, err := a.db.CreateServer(ctx, admin.user, store.Server{ID: next, Name: "New", Hostname: "192.0.2.200", SSHPort: 22, SSHUser: "root", Fingerprint: "test"}, []byte("fake"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.Pool.Exec(ctx, `UPDATE servers SET vpn_state='ready' WHERE id=$1`, next); err != nil {
		t.Fatal(err)
	}
	if err = a.db.SetDefaultServer(ctx, admin.user, next); err != nil {
		t.Fatal(err)
	}
	n, err := a.db.SendReminders(ctx, admin.user)
	if err != nil || n != 0 {
		t.Fatal("auto/manual duplicate", n, err)
	}
	audience, err := a.db.ReminderAudience(ctx)
	if err != nil || len(audience) != 1 {
		t.Fatal("audience", err)
	}
	if err = a.db.QueuePush(ctx); err != nil {
		t.Fatal(err)
	}
	// User reissued before delivery. It is no longer a useful reminder.
	if _, err = a.db.Pool.Exec(ctx, `UPDATE profiles SET desired=false WHERE device_id=$1`, d); err != nil {
		t.Fatal(err)
	}
	job, err := a.db.ClaimPush(ctx)
	if err != nil || job.Eligible {
		t.Fatal("stale reminder not suppressed", err, old)
	}
	prefs := store.PushPreferences{Outages: false, Recovery: false, Reminders: false, Operations: false}
	if err = a.db.SavePushPreferences(ctx, u.user, prefs); err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.Pool.Exec(ctx, `INSERT INTO notifications(user_id,event_key,kind,severity,server_id,incident_id) VALUES($1,'incident','outage','critical',$2,$3);`, u.user.ID, old, cryptobox.ID()); err != nil {
		t.Fatal(err)
	}
	// Public payloads never expose private endpoints or diagnostic error text.
	r := request(a, "GET", "/api/notifications", u, "")
	requireStatus(t, r, 200)
	if strings.Contains(r.Body.String(), "192.0.2") || strings.Contains(r.Body.String(), "secret-capability") {
		t.Fatal("notification leak")
	}
}

func TestSelfPushTargetsOnlyCurrentBrowser(t *testing.T) {
	a := testApp(t)
	u, other := account(t, a, "user"), account(t, a, "user")
	ctx := context.Background()
	endpoints := []string{"https://fcm.googleapis.com/fcm/send/phone", "https://fcm.googleapis.com/fcm/send/laptop"}
	for _, endpoint := range endpoints {
		requireStatus(t, request(a, "POST", "/api/push/subscription", u, pushBody(endpoint)), 204)
	}
	requireStatus(t, request(a, "POST", "/api/push/test", other, `{"endpoint":"`+endpoints[0]+`"}`), 404)
	requireStatus(t, request(a, "POST", "/api/push/test", u, `{"endpoint":"`+endpoints[0]+`"}`), 202)
	if err := a.db.QueuePush(ctx); err != nil {
		t.Fatal(err)
	}
	j, err := a.db.ClaimPush(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var hash string
	if err = a.db.Pool.QueryRow(ctx, `SELECT endpoint_hash FROM push_subscriptions WHERE id=$1`, j.SubscriptionID).Scan(&hash); err != nil || hash != cryptobox.Hash(endpoints[0]) {
		t.Fatal("wrong browser", err)
	}
	if _, err = a.db.ClaimPush(ctx); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("test also sent to another device", err)
	}
	if err = a.db.FinishPush(ctx, j, 201, false); err != nil {
		t.Fatal(err)
	}
	payload := `{"endpoint":"` + endpoints[0] + `"}`
	r := request(a, "POST", "/api/push/status", u, payload)
	requireStatus(t, r, 200)
	var status store.PushStatus
	if err = json.Unmarshal(r.Body.Bytes(), &status); err != nil || !status.Registered || status.Status != "accepted" || status.Code != 201 {
		t.Fatal("test transport status", err)
	}
	r = request(a, "POST", "/api/push/status", other, payload)
	requireStatus(t, r, 200)
	if err = json.Unmarshal(r.Body.Bytes(), &status); err != nil || status.Registered || status.Status != "" {
		t.Fatal("cross-user test status leaked", err)
	}

}
