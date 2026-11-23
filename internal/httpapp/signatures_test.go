package httpapp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/NetherQuartz/cu-chi/internal/awg"
	"github.com/NetherQuartz/cu-chi/internal/store"
)

func TestSignatureRollout(t *testing.T) {
	a, admin, id, w, _ := vpnSetup(t)
	ctx := context.Background()
	alice, other := account(t, a, "user"), account(t, a, "user")
	device := deviceFor(t, a, alice, "Phone")
	p := issueFor(t, a, alice, device)
	applyJob(t, a, w, "")
	endpoint := "/api/admin/servers/" + id + "/signature"
	path := "/api/profiles/" + p.ID
	export := func() string {
		t.Helper()
		r := request(a, "GET", path+"/download", alice, "")
		requireStatus(t, r, 200)
		return r.Body.String()
	}
	profiles := func() store.Profile {
		t.Helper()
		r := request(a, "GET", "/api/profiles", alice, "")
		requireStatus(t, r, 200)
		var rows []store.Profile
		if err := json.Unmarshal(r.Body.Bytes(), &rows); err != nil || len(rows) != 1 {
			t.Fatal("missing profile")
		}
		return rows[0]
	}
	original := export()
	if !strings.Contains(original, "I1 = "+awg.DefaultSignature()+"\n") || profiles().UpdateRequired {
		t.Fatal("wrong initial export")
	}
	set := func(user identity, revision int, mode, value string, status int) {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"mode": mode, "custom": value, "revision": revision})
		requireStatus(t, request(a, "PUT", endpoint, user, string(b)), status)
	}
	set(other, 1, "off", "", 403)
	set(admin, 1, "custom", "<r 10>\nPostUp = command", 400)
	set(admin, 1, "custom", "<r 1401>", 400)
	set(admin, 1, "custom", "<b 0xa>", 400)
	requireStatus(t, request(a, "POST", "/api/push/subscription", alice, pushBody("https://fcm.googleapis.com/fcm/send/fixture-signature")), 204)
	set(admin, 1, "off", "", 204)
	set(admin, 1, "off", "", 204)
	if !profiles().UpdateRequired || profiles().SignatureRevision != 2 {
		t.Fatal("missing pending update")
	}
	before := strings.SplitN(original, "[Interface]", 2)
	after := strings.SplitN(export(), "[Interface]", 2)
	if len(before) != 2 || len(after) != 2 || strings.Replace(before[1], "I1 = "+awg.DefaultSignature()+"\n", "", 1) != after[1] {
		t.Fatal("unexpected key or I1 in updated export")
	}
	ack := func(user identity, rev, status int) {
		t.Helper()
		requireStatus(t, request(a, "POST", path+"/confirm-import", user, fmt.Sprintf(`{"revision":%d}`, rev)), status)
	}
	ack(other, 2, 404)
	ack(alice, 1, 409)
	var count int
	if err := a.db.Pool.QueryRow(ctx, "SELECT count(*) FROM notifications WHERE kind='awg_profile_update' AND user_id=$1", alice.user.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate or missing notice", err)
	}
	if r := request(a, "GET", "/api/notifications", other, ""); strings.Contains(r.Body.String(), "awg_profile_update") {
		t.Fatal("wrong audience")
	}
	if err := a.db.QueuePush(ctx); err != nil {
		t.Fatal(err)
	}
	j, err := a.db.ClaimPush(ctx)
	if err != nil || !j.Eligible || j.Kind != "awg_profile_update" {
		t.Fatal("push unavailable", err)
	}
	if err = a.db.FinishPush(ctx, j, 201, false); err != nil {
		t.Fatal(err)
	}
	ack(alice, 2, 204)
	if profiles().UpdateRequired {
		t.Fatal("acknowledgement not recorded")
	}
	set(admin, 1, "custom", "<r 4>", 409)
	sig := "<b 0x0102><r 24><rd 3><rc 4><t>"
	set(admin, 2, "custom", sig, 204)
	if !strings.Contains(export(), "I1 = "+sig+"\n") {
		t.Fatal("custom signature not exported")
	}
	requireStatus(t, request(a, "GET", path+"/qr", alice, ""), 200)
	if err = a.db.QueuePush(ctx); err != nil {
		t.Fatal(err)
	}
	ack(alice, 3, 204)
	j, err = a.db.ClaimPush(ctx)
	if err != nil || j.Eligible {
		t.Fatal("obsolete push was eligible", err)
	}
	if _, err = a.db.Claim(ctx); err == nil {
		t.Fatal("client-only rollout enqueued an SSH job")
	}
}
