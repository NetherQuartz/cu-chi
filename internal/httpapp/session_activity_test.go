package httpapp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/NetherQuartz/cu-chi/internal/cryptobox"
	"github.com/jackc/pgx/v5"
)

func TestSessionActivityRenewsOnlyAuthorizedLiveSession(t *testing.T) {
	a := testApp(t)
	i := account(t, a, "user")
	other := account(t, a, "user")
	ctx := context.Background()
	old := time.Now().Add(2 * time.Minute).Truncate(time.Microsecond)
	if _, err := a.db.Pool.Exec(ctx, "UPDATE sessions SET expires_at=$1 WHERE token_hash=$2", old, cryptobox.Hash(i.token)); err != nil {
		t.Fatal(err)
	}
	expiry := func(token string) time.Time {
		t.Helper()
		s, err := a.db.Session(ctx, cryptobox.Hash(token))
		if err != nil {
			t.Fatal(err)
		}
		return s.Expires
	}
	otherExpiry := expiry(other.token)
	for _, path := range []string{"/api/me", "/api/devices", "/api/profiles", "/api/notifications"} {
		requireStatus(t, request(a, "GET", path, i, ""), 200)
	}
	if !expiry(i.token).Equal(old) {
		t.Fatal("background reads renewed idle session")
	}
	for _, bad := range []struct{ origin, csrf string }{{"https://evil.example", i.csrf}, {a.cfg.PublicURL, ""}, {a.cfg.PublicURL, "wrong"}} {
		r := httptest.NewRequest("POST", "/api/session/activity", nil)
		r.AddCookie(&http.Cookie{Name: "cuchi_session", Value: i.token})
		r.Header.Set("Origin", bad.origin)
		r.Header.Set("X-CSRF-Token", bad.csrf)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		requireStatus(t, w, 403)
		if len(w.Result().Cookies()) != 0 || !expiry(i.token).Equal(old) {
			t.Fatal("rejected request renewed session")
		}
	}
	requireStatus(t, request(a, "POST", "/api/session/activity", identity{}, ""), 401)
	// Cookie policy follows production even though the fixture used dev login.
	a.cfg.DevAuth = false
	w := request(a, "POST", "/api/session/activity", i, "")
	requireStatus(t, w, 200)
	if left := time.Until(expiry(i.token)); left < 29*time.Minute || left > 30*time.Minute {
		t.Fatalf("expiry not extended 30 minutes: %s", left)
	}
	if !expiry(other.token).Equal(otherExpiry) {
		t.Fatal("activity renewed another browser session")
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "cuchi_session" || cookies[0].Value != i.token || cookies[0].MaxAge < 1790 || cookies[0].MaxAge > 1800 || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatal("incorrect renewed session cookie")
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("renewal response cacheable")
	}
	// Exercise store directly too: expiry/logout/denial between auth and UPDATE
	// must not resurrect the session or manufacture a fresh cookie.
	for _, scenario := range []string{"expired", "deleted", "denied"} {
		t.Run(scenario, func(t *testing.T) {
			user := account(t, a, "user")
			var err error
			switch scenario {
			case "expired":
				_, err = a.db.Pool.Exec(ctx, "UPDATE sessions SET expires_at=now()-interval '1 second' WHERE token_hash=$1", cryptobox.Hash(user.token))
			case "deleted":
				err = a.db.DeleteSession(ctx, cryptobox.Hash(user.token))
			case "denied":
				_, err = a.db.Pool.Exec(ctx, "UPDATE users SET role='denied' WHERE id=$1", user.user.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = a.db.RenewSession(ctx, cryptobox.Hash(user.token)); !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("invalid session renewed: %v", err)
			}
			denied := request(a, "POST", "/api/session/activity", user, "")
			requireStatus(t, denied, 401)
			if len(denied.Result().Cookies()) != 0 {
				t.Fatal("rejected renewal set a cookie")
			}
		})
	}
}
