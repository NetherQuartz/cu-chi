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
