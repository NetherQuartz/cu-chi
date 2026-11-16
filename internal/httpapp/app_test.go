package httpapp

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/ssh"
	"golang.org/x/oauth2"

	"github.com/NetherQuartz/cu-chi/internal/config"
	"github.com/NetherQuartz/cu-chi/internal/cryptobox"
	"github.com/NetherQuartz/cu-chi/internal/store"
)

func testDB(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx := context.Background()
	root, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "test_" + strings.ReplaceAll(cryptobox.ID(), "-", "")
	if _, err = root.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		root.Close()
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := store.Open(ctx, u.String())
	if err != nil {
		root.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		root.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Pool.Close()
		root.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		root.Close()
	})
	return db
}
func testApp(t *testing.T) *App {
	t.Helper()
	db := testDB(t)
	a, err := New(context.Background(), config.Config{DevAuth: true, MasterKey: bytes.Repeat([]byte{1}, 32), PublicURL: "http://127.0.0.1:8080", AdminGroup: "administrators", UserGroup: "vpn-users"}, db)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

type identity struct {
	token, csrf string
	user        store.User
}

func account(t *testing.T, a *App, role string) identity {
	t.Helper()
	u, err := a.db.UpsertUser(context.Background(), "test", cryptobox.Token(), role, role)
	if err != nil {
		t.Fatal(err)
	}
	i := identity{cryptobox.Token(), cryptobox.Token(), u}
	if err = a.db.CreateSession(context.Background(), cryptobox.Hash(i.token), u.ID, i.csrf, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return i
}
func request(a *App, method, path string, i identity, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if i.token != "" {
		r.AddCookie(&http.Cookie{Name: "cuchi_session", Value: i.token})
	}
	r.Header.Set("Origin", a.cfg.PublicURL)
	r.Header.Set("X-CSRF-Token", i.csrf)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w
}
func requireStatus(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d, want %d: %s", w.Code, status, w.Body.String())
	}
}
func TestOwnershipAndRoles(t *testing.T) {
	a := testApp(t)
	alice := account(t, a, "user")
	bob := account(t, a, "user")
	admin := account(t, a, "admin")
	requireStatus(t, request(a, "GET", "/api/devices", identity{}, ""), 401)
	requireStatus(t, request(a, "GET", "/api/admin/servers", alice, ""), 403)
	requireStatus(t, request(a, "POST", "/api/devices", alice, `{"name":"Router","platform":"gateway"}`), 403)
	created := request(a, "POST", "/api/devices", alice, `{"name":"Телефон","platform":"ios"}`)
	requireStatus(t, created, 201)
	var d store.Device
	if err := json.Unmarshal(created.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, request(a, "DELETE", "/api/devices/"+d.ID, bob, ""), 404)
	other := request(a, "GET", "/api/devices", bob, "")
	requireStatus(t, other, 200)
	if strings.TrimSpace(other.Body.String()) != "[]" {
		t.Fatal("another user's devices leaked")
	}
	all := request(a, "GET", "/api/devices", admin, "")
	requireStatus(t, all, 200)
	if !strings.Contains(all.Body.String(), d.ID) {
		t.Fatal("admin cannot see registered device")
	}
	requireStatus(t, request(a, "POST", "/api/devices", alice, `{"name":"Телефон","platform":"ios"}`), 409)
	requireStatus(t, request(a, "DELETE", "/api/devices/"+d.ID, admin, ""), 204)
	events := request(a, "GET", "/api/admin/events", admin, "")
	if !strings.Contains(events.Body.String(), "device.deleted") {
		t.Fatal("missing deletion audit event")
	}
}
func TestMutationProtectionAndSessionExpiry(t *testing.T) {
	a := testApp(t)
	i := account(t, a, "user")
	for _, test := range []struct{ origin, csrf string }{{"https://evil.example", i.csrf}, {a.cfg.PublicURL, ""}, {a.cfg.PublicURL, "wrong"}} {
		r := httptest.NewRequest("POST", "/api/devices", strings.NewReader(`{"name":"Laptop","platform":"macos"}`))
		r.AddCookie(&http.Cookie{Name: "cuchi_session", Value: i.token})
		r.Header.Set("Origin", test.origin)
		r.Header.Set("X-CSRF-Token", test.csrf)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		requireStatus(t, w, 403)
	}
	requireStatus(t, request(a, "POST", "/api/devices", i, `{"name":"Laptop","platform":"macos","user_id":"someone-else"}`), 400)
	requireStatus(t, request(a, "POST", "/api/devices", i, `{"name":"Laptop","platform":"macos"}{}`), 400)
	if _, err := a.db.Pool.Exec(context.Background(), "UPDATE sessions SET expires_at=now()-interval '1 second' WHERE user_id=$1", i.user.ID); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, request(a, "GET", "/api/me", i, ""), 401)
	fresh := account(t, a, "user")
	requireStatus(t, request(a, "POST", "/api/logout", fresh, ""), 204)
	requireStatus(t, request(a, "GET", "/api/me", fresh, ""), 401)
	requireStatus(t, request(a, "GET", "/auth/dev", identity{}, ""), 405)
}
func TestServerSecretsAreEncryptedAndNotReturned(t *testing.T) {
	a := testApp(t)
	admin := account(t, a, "admin")
	user := account(t, a, "user")
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(key, "test only")
	if err != nil {
		t.Fatal(err)
	}
	privateKey := string(pem.EncodeToMemory(block))
	hostKey, _ := ssh.NewPublicKey(key.Public())
	fingerprint := ssh.FingerprintSHA256(hostKey)
	payload, _ := json.Marshal(map[string]any{"name": "Test VPS", "hostname": "192.0.2.10", "ssh_port": 22, "ssh_user": "root", "host_key_fingerprint": fingerprint, "private_key": privateKey})
	requireStatus(t, request(a, "POST", "/api/admin/servers", user, string(payload)), 403)
	w := request(a, "POST", "/api/admin/servers", admin, string(payload))
	requireStatus(t, w, 201)
	var server store.Server
	if err = json.Unmarshal(w.Body.Bytes(), &server); err != nil {
		t.Fatal(err)
	}
	var encrypted []byte
	if err = a.db.Pool.QueryRow(context.Background(), "SELECT encrypted_private_key FROM servers WHERE id=$1", server.ID).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte("PRIVATE KEY")) {
		t.Fatal("plaintext secret in database")
	}
	decrypted, err := a.box.Open(encrypted, "server:"+server.ID)
	if err != nil || string(decrypted) != privateKey {
		t.Fatal("encrypted key cannot be recovered")
	}
	for _, response := range []*httptest.ResponseRecorder{w, request(a, "GET", "/api/admin/servers", admin, ""), request(a, "GET", "/api/admin/events", admin, "")} {
		if strings.Contains(response.Body.String(), "PRIVATE KEY") || strings.Contains(response.Body.String(), "encrypted_private_key") {
			t.Fatal("secret in API response")
		}
	}
}
func TestRoleMappingFailsClosed(t *testing.T) {
	for _, test := range []struct {
		groups []string
		want   string
	}{{nil, "denied"}, {[]string{"users"}, "denied"}, {[]string{"Administrators"}, "denied"}, {[]string{"vpn-users"}, "user"}, {[]string{"vpn-users", "administrators"}, "admin"}} {
		if got := roleFor(test.groups, "administrators", "vpn-users"); got != test.want {
			t.Fatalf("groups=%v: got %s", test.groups, got)
		}
	}
}
func TestOIDCLoginVerification(t *testing.T) {
	a := testApp(t)
	a.cfg.DevAuth = false
	a.cfg.PublicURL = "https://portal.example.test"
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	const issuer = "https://sso.example.test/webman/sso"
	var nonce, challenge, mode string
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if oauth2.S256ChallengeFromVerifier(r.Form.Get("code_verifier")) != challenge {
			t.Error("PKCE verifier mismatch")
			w.WriteHeader(400)
			return
		}
		claims := map[string]any{"iss": issuer, "sub": "alice", "aud": "cuchi-test", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "nonce": nonce, "username": "Alice", "groups": []string{"vpn-users"}}
		switch mode {
		case "wrong nonce":
			claims["nonce"] = "wrong"
		case "wrong audience":
			claims["aud"] = "another-app"
		case "expired":
			claims["exp"] = time.Now().Add(-time.Hour).Unix()
		case "no groups":
			delete(claims, "groups")
		}
		token, err := jwt.Signed(signer).Claims(claims).Serialize()
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		jsonResponse(w, 200, map[string]any{"access_token": "test-access", "token_type": "Bearer", "id_token": token})
	}))
	defer endpoint.Close()
	a.oauth = &oauth2.Config{ClientID: "cuchi-test", ClientSecret: "test", RedirectURL: a.cfg.PublicURL + "/auth/callback", Scopes: []string{"openid", "groups"}, Endpoint: oauth2.Endpoint{AuthURL: issuer + "/authorize", TokenURL: endpoint.URL, AuthStyle: oauth2.AuthStyleInHeader}}
	a.verifier = oidc.NewVerifier(issuer, &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&key.PublicKey}}, &oidc.Config{ClientID: "cuchi-test"})
	for _, test := range []struct {
		mode   string
		status int
	}{{"ok", 303}, {"wrong nonce", 401}, {"wrong audience", 401}, {"expired", 401}, {"no groups", 403}} {
		t.Run(test.mode, func(t *testing.T) {
			mode = test.mode
			login := request(a, "GET", "/auth/login", identity{}, "")
			requireStatus(t, login, 302)
			redirect, err := url.Parse(login.Header().Get("Location"))
			if err != nil {
				t.Fatal(err)
			}
			nonce = redirect.Query().Get("nonce")
			challenge = redirect.Query().Get("code_challenge")
			state := redirect.Query().Get("state")
			if nonce == "" || challenge == "" || redirect.Query().Get("code_challenge_method") != "S256" {
				t.Fatal("missing OIDC nonce or PKCE")
			}
			cookies := login.Result().Cookies()
			if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly {
				t.Fatal("insecure login cookie")
			}
			path := "/auth/callback?code=test&state=" + url.QueryEscape(state)
			noCookie := request(a, "GET", path, identity{}, "")
			requireStatus(t, noCookie, 400)
			r := httptest.NewRequest("GET", path, nil)
			r.AddCookie(cookies[0])
			w := httptest.NewRecorder()
			a.Handler().ServeHTTP(w, r)
			requireStatus(t, w, test.status)
			if test.mode == "ok" {
				found := false
				for _, c := range w.Result().Cookies() {
					if c.Name == "cuchi_session" {
						found = true
						requireStatus(t, request(a, "GET", "/api/me", identity{token: c.Value}, ""), 200)
					}
				}
				if !found {
					t.Fatal("missing authenticated session")
				}
			}
			replay := httptest.NewRecorder()
			a.Handler().ServeHTTP(replay, r)
			requireStatus(t, replay, 400)
		})
	}
	requireStatus(t, request(a, "POST", "/auth/dev", identity{}, ""), 404)
}
func Example_roleFor() {
	fmt.Println(roleFor([]string{"vpn-users"}, "administrators", "vpn-users")) // Output: user
}
