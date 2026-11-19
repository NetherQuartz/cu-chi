package httpapp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/NetherQuartz/cu-chi/internal/store"
)

func TestXraySubscriptionLifecycle(t *testing.T) {
	a, admin, server, _, _ := vpnSetup(t)
	ctx := context.Background()
	alice, bob := account(t, a, "user"), account(t, a, "user")
	dev := deviceFor(t, a, alice, "Phone")
	deploy := "/api/admin/servers/" + server + "/xray"
	profile := "/api/devices/" + dev + "/xray"
	requireStatus(t, request(a, "POST", deploy, bob, ""), 403)
	requireStatus(t, request(a, "POST", deploy, admin, ""), 204)
	job, e := a.db.XNext(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.db.XFinish(ctx, job, "www.example.com", ""); e != nil {
		t.Fatal(e)
	}
	requireStatus(t, request(a, "POST", profile, bob, ""), 404)
	requireStatus(t, request(a, "POST", profile, alice, ""), 204)
	requireStatus(t, request(a, "GET", profile+"/import", alice, ""), 409)
	job, e = a.db.XNext(ctx)
	if e != nil || len(job.Clients) != 1 {
		t.Fatal("missing desired device", e)
	}
	if e = a.db.XFinish(ctx, job, "www.example.com", ""); e != nil {
		t.Fatal(e)
	}
	requireStatus(t, request(a, "GET", profile+"/import", bob, ""), 404)
	r := request(a, "GET", profile+"/import", alice, "")
	requireStatus(t, r, 200)
	var data struct {
		Subscription string
		Links        []string
	}
	if e = json.Unmarshal(r.Body.Bytes(), &data); e != nil {
		t.Fatal(e)
	}
	sub, e := url.Parse(data.Subscription)
	if e != nil {
		t.Fatal(e)
	}
	r = request(a, "GET", sub.Path, identity{}, "")
	requireStatus(t, r, 200)
	decoded, e := base64.StdEncoding.DecodeString(r.Body.String())
	if e != nil || len(data.Links) != 1 || string(decoded) != data.Links[0] || !strings.HasPrefix(string(decoded), "vless://") {
		t.Fatal("subscription/import disagree")
	}
	if e = a.db.DeleteDevice(ctx, alice.user, dev); e != store.ErrConflict {
		t.Fatal("deleted live device", e)
	}
	requireStatus(t, request(a, "DELETE", profile, alice, ""), 204)
	requireStatus(t, request(a, "GET", sub.Path, identity{}, ""), 404)
	job, e = a.db.XNext(ctx)
	if e != nil || len(job.Clients) != 0 {
		t.Fatal("revoked client in snapshot", e)
	}
	if e = a.db.XFinish(ctx, job, "www.example.com", ""); e != nil {
		t.Fatal(e)
	}
	if e = a.db.DeleteDevice(ctx, alice.user, dev); e != nil {
		t.Fatal(e)
	}
}

func TestXrayAvailabilityAnnouncement(t *testing.T) {
	for _, hidden := range []bool{false, true} {
		t.Run(map[bool]string{false: "public deployment", true: "publish after testing"}[hidden], func(t *testing.T) {
			a, admin, server, _, _ := vpnSetup(t)
			ctx := context.Background()
			user, muted, denied := account(t, a, "user"), account(t, a, "user"), account(t, a, "denied")
			for _, who := range []identity{user, muted} {
				requireStatus(t, request(a, "POST", "/api/push/subscription", who, pushBody("https://fcm.googleapis.com/fcm/send/"+who.user.ID)), 204)
			}
			requireStatus(t, request(a, "PUT", "/api/push/preferences", muted, `{"reminders":false}`), 204)
			inbox := func(who identity, want int) {
				t.Helper()
				notices, err := a.db.Inbox(ctx, who.user)
				if err != nil || len(notices) != want {
					t.Fatalf("inbox: got %d, want %d: %v", len(notices), want, err)
				}
				if want > 0 && (notices[0].Kind != "xray_available" || notices[0].ServerName != "Test VPS" || notices[0].Severity != "info") {
					t.Fatalf("unexpected notice %#v", notices[0])
				}
			}
			deploy := "/api/admin/servers/" + server + "/xray"
			body := ""
			if hidden {
				body = `{"published":false}`
			}
			requireStatus(t, request(a, "POST", deploy, admin, body), 204)
			job, err := a.db.XNext(ctx)
			if err != nil {
				t.Fatal(err)
			}
			finish := func(failure string) {
				t.Helper()
				if err := a.db.XFinish(ctx, job, "www.example.com", failure); err != nil {
					t.Fatal(err)
				}
			}
			publish := func(body string) {
				t.Helper()
				requireStatus(t, request(a, "POST", deploy+"-publication", admin, body), 204)
			}
			finish("probe failed")
			inbox(user, 0)
			finish("")
			if hidden {
				inbox(user, 0)
				publish(`{"published":true}`)
			}
			inbox(user, 1)
			inbox(admin, 1)
			inbox(muted, 1)
			inbox(denied, 0)
			if err = a.db.QueuePush(ctx); err != nil {
				t.Fatal(err)
			}
			var count int
			if err = a.db.Pool.QueryRow(ctx, "SELECT count(*) FROM push_outbox").Scan(&count); err != nil || count != 1 {
				t.Fatal("wrong push audience", count, err)
			}
			publish(`{"published":false}`)
			pushJob, err := a.db.ClaimPush(ctx)
			if err != nil || pushJob.Eligible || pushJob.Kind != "xray_available" || pushJob.ServerName != "Test VPS" {
				t.Fatal("hidden server eligible or missing push context", err)
			}
			if err = a.db.FinishPush(ctx, pushJob, 0, true); err != nil {
				t.Fatal(err)
			}
			later := account(t, a, "user")
			publish(`{"published":true}`)
			finish("")
			requireStatus(t, request(a, "POST", deploy, admin, ""), 204)
			job, err = a.db.XNext(ctx)
			if err != nil {
				t.Fatal(err)
			}
			finish("")
			inbox(user, 1)
			inbox(later, 0)
		})
	}
}
