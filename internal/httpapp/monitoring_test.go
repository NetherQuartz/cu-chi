package httpapp

import (
	"context"
	"encoding/json"
	"github.com/NetherQuartz/cu-chi/internal/awg"
	"github.com/NetherQuartz/cu-chi/internal/cryptobox"
	"github.com/NetherQuartz/cu-chi/internal/store"
	"github.com/NetherQuartz/cu-chi/internal/telemetry"
	"strings"
	"testing"
	"time"
)

func TestTrafficOwnershipRotationResetsAndRetention(t *testing.T) {
	a, admin, server, w, _ := vpnSetup(t)
	ctx := context.Background()
	alice, bob := account(t, a, "user"), account(t, a, "user")
	d := deviceFor(t, a, alice, "Alice phone")
	p := issueFor(t, a, alice, d)
	applyJob(t, a, w, "")
	db := deviceFor(t, a, bob, "Bob phone")
	pb := issueFor(t, a, bob, db)
	applyJob(t, a, w, "")
	at := time.Now().UTC().Add(-10 * time.Minute)
	sample := telemetry.Sample{At: at, Boot: "boot", Epoch: "runtime", Interface: "eth0", CPU: 100, Idle: 20, Cores: 2, MemoryTotal: 1000, MemoryUsed: 400, DiskTotal: 1000, DiskUsed: 200, Tunnel: true, Peers: []telemetry.Peer{{Key: p.PublicKey, RX: 100, TX: 200, Handshake: at.Unix()}, {Key: pb.PublicKey, RX: 800, TX: 900}}}
	save := func() {
		t.Helper()
		if err := a.db.SaveObservation(ctx, server, sample, ""); err != nil {
			t.Fatal(err)
		}
	}
	read := func(i identity, path string) store.Traffic {
		t.Helper()
		r := request(a, "GET", path, i, "")
		requireStatus(t, r, 200)
		var v store.Traffic
		if err := json.Unmarshal(r.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	save()
	sample.At = sample.At.Add(time.Minute)
	sample.Peers[0].RX += 60
	sample.Peers[0].TX += 120
	sample.Peers[1].RX += 600
	save()
	save() // Retry must not double count.
	result := read(alice, "/api/traffic?period=day")
	if len(result.Rows) != 1 || result.Rows[0].Upload != 60 || result.Rows[0].Download != 120 {
		t.Fatalf("ownership/deltas: %+v", result.Rows)
	}
	if len(read(alice, "/api/traffic?device_id="+db).Rows) != 0 {
		t.Fatal("cross-user device leaked")
	}
	requireStatus(t, request(a, "GET", "/api/traffic?user_id="+bob.user.ID, alice, ""), 403)
	requireStatus(t, request(a, "GET", "/api/admin/monitoring", alice, ""), 403)
	if len(read(admin, "/api/traffic").Rows) != 2 {
		t.Fatal("admin missing user aggregate")
	}
	for _, point := range result.History {
		if point.Upload != 0 && point.Upload != 60 {
			t.Fatal("foreign history leaked")
		}
	}
	sample.At = sample.At.Add(time.Minute)
	sample.Epoch = "restart"
	sample.Peers[0].RX = 90000
	sample.Peers[0].TX = 90000
	save()
	if v := read(alice, "/api/traffic"); v.Rows[0].Upload != 60 {
		t.Fatal("restart introduced a spike")
	}
	sample.At = sample.At.Add(4 * time.Minute)
	sample.Peers[0].RX += 10
	save()
	if v := read(alice, "/api/traffic"); v.Rows[0].Upload != 70 || v.Rows[0].Gaps < 2 {
		t.Fatal("gap accounting lost", v)
	}
	p2 := issueFor(t, a, alice, d)
	applyJob(t, a, w, "")
	sample.At = sample.At.Add(time.Minute)
	sample.Peers[0] = telemetry.Peer{Key: p2.PublicKey}
	save()
	sample.At = sample.At.Add(time.Minute)
	sample.Peers[0].RX = 30
	sample.Peers[0].TX = 40
	save()
	if v := read(alice, "/api/traffic"); len(v.Rows) != 1 || v.Rows[0].Upload != 100 || v.Rows[0].Download != 160 {
		t.Fatal("rotation lost totals", v)
	}
	// Add a second server/profile: one device now has two independent pair totals.
	other := cryptobox.ID()
	_, err := a.db.CreateServer(ctx, admin.user, store.Server{ID: other, Name: "Second VPS", Hostname: "192.0.2.11", SSHPort: 22, SSHUser: "root", Fingerprint: "test"}, []byte("test"))
	if err != nil {
		t.Fatal(err)
	}
	key := awg.NewKeys().Public
	_, err = a.db.Pool.Exec(ctx, `INSERT INTO profiles(id,device_id,server_id,revision,address,public_key,encrypted_keys,desired,state) VALUES($1,$2,$3,3,'10.66.0.9',$4,$5,false,'ready')`, cryptobox.ID(), d, other, key, []byte("test"))
	if err != nil {
		t.Fatal(err)
	}
	next := sample
	next.Peers = []telemetry.Peer{{Key: key}}
	if err = a.db.SaveObservation(ctx, other, next, ""); err != nil {
		t.Fatal(err)
	}
	next.At = next.At.Add(time.Minute)
	next.Peers[0].RX = 20
	if err = a.db.SaveObservation(ctx, other, next, ""); err != nil {
		t.Fatal(err)
	}
	if v := read(alice, "/api/traffic?server_id="+other); len(v.Rows) != 1 || v.Rows[0].Upload != 20 {
		t.Fatal("pair filter", v)
	}
	v := read(alice, "/api/traffic?device_id="+d)
	var sum int64
	for _, r := range v.Rows {
		sum += r.Upload
	}
	if len(v.Rows) != 2 || sum != 120 {
		t.Fatal("device across servers", v)
	}
	// Device deletion must not delete measured history or expose it to other owners.
	requireStatus(t, request(a, "POST", "/api/devices/"+d+"/revoke", alice, ""), 202)
	if _, err = a.db.Pool.Exec(ctx, `UPDATE profiles SET state='revoked',desired=false WHERE device_id=$1`, d); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, request(a, "DELETE", "/api/devices/"+d, alice, ""), 204)
	if len(read(alice, "/api/traffic").Rows) != 2 {
		t.Fatal("deleted device history lost")
	}
	if _, err = a.db.Pool.Exec(ctx, `UPDATE traffic_samples SET at=at-interval '40 days'; UPDATE host_samples SET at=at-interval '40 days'`); err != nil {
		t.Fatal(err)
	}
	if err = a.db.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	v = read(alice, "/api/traffic")
	sum = 0
	for _, r := range v.Rows {
		if r.Upload != 0 {
			t.Fatal("expired details retained")
		}
		sum += r.LifetimeUpload
	}
	if sum != 120 {
		t.Fatal("retention deleted lifetime totals")
	}
}
func TestLocationAndMonitoringAPI(t *testing.T) {
	a, admin, server, _, _ := vpnSetup(t)
	user := account(t, a, "user")
	path := "/api/admin/servers/" + server + "/location"
	body := `{"country_code":"NL","city":"Amsterdam"}`
	requireStatus(t, request(a, "PATCH", path, user, body), 403)
	bad := admin
	bad.csrf = ""
	requireStatus(t, request(a, "PATCH", path, bad, body), 403)
	for _, payload := range []string{`{"country_code":"XX","city":"test"}`, `{"country_code":"NL","city":"a\nb"}`} {
		requireStatus(t, request(a, "PATCH", path, admin, payload), 400)
	}
	requireStatus(t, request(a, "PATCH", path, admin, body), 204)
	services := request(a, "GET", "/api/services", user, "")
	requireStatus(t, services, 200)
	if !strings.Contains(services.Body.String(), "Amsterdam") || strings.Contains(services.Body.String(), "hostname") || strings.Contains(services.Body.String(), "192.0.2") {
		t.Fatal("public metadata/redaction")
	}
	for _, url := range []string{"/api/admin/monitoring", "/api/admin/servers/" + server + "/history?period=hour", "/api/traffic?period=month", "/flags/nl.svg", "/countries.json"} {
		requireStatus(t, request(a, "GET", url, admin, ""), 200)
	}
	requireStatus(t, request(a, "GET", "/flags/xx.svg", admin, ""), 404)
	requireStatus(t, request(a, "GET", "/api/traffic?period=invalid", user, ""), 400)
	requireStatus(t, request(a, "GET", "/api/traffic?server_id=invalid", user, ""), 400)
}
