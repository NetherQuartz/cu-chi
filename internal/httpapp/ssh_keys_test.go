package httpapp

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"github.com/NetherQuartz/cu-chi/internal/cryptobox"
	"github.com/NetherQuartz/cu-chi/internal/deployer"
	"github.com/NetherQuartz/cu-chi/internal/store"
	"github.com/NetherQuartz/cu-chi/internal/worker"
	"golang.org/x/crypto/ssh"
	"strings"
	"testing"
)

type credentialCheck struct {
	t      *testing.T
	secret []byte
	method string
	called bool
}

func (f *credentialCheck) Check(_ context.Context, c deployer.Credentials) error {
	f.called = true
	secret := c.Key
	if c.AuthMethod == "password" {
		secret = c.Password
	}
	if c.AuthMethod != f.method || !bytes.Equal(secret, f.secret) {
		f.t.Fatal("worker passed wrong credential")
	}
	return nil
}
func (f *credentialCheck) Apply(context.Context, deployer.Credentials, deployer.Payload) error {
	f.t.Fatal("unexpected deployment")
	return nil
}
func TestSavedKeysAndPasswordAccess(t *testing.T) {
	a := testApp(t)
	admin := account(t, a, "admin")
	user := account(t, a, "user")
	ctx := context.Background()
	post := func(data any) string {
		b, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	for _, method := range []string{"GET", "POST", "DELETE"} {
		path := "/api/admin/ssh-keys"
		if method == "DELETE" {
			path += "/" + cryptobox.ID()
		}
		requireStatus(t, request(a, method, path, user, `{"name":"Shared","mode":"generate"}`), 403)
	}
	noCSRF := admin
	noCSRF.csrf = ""
	requireStatus(t, request(a, "POST", "/api/admin/ssh-keys", noCSRF, `{"name":"Shared","mode":"generate"}`), 403)
	response := request(a, "POST", "/api/admin/ssh-keys", admin, `{"name":"Shared","mode":"generate"}`)
	requireStatus(t, response, 201)
	var k store.SSHKey
	if err := json.Unmarshal(response.Body.Bytes(), &k); err != nil {
		t.Fatal(err)
	}
	var encrypted []byte
	if err := a.db.Pool.QueryRow(ctx, "SELECT encrypted_private_key FROM ssh_keys WHERE id=$1", k.ID).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	private, err := a.box.Open(encrypted, "ssh-key:"+k.ID)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(k.PublicKey, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))) || k.Fingerprint != ssh.FingerprintSHA256(signer.PublicKey()) {
		t.Fatal("public/private key mismatch")
	}
	if bytes.Contains(encrypted, []byte("PRIVATE KEY")) {
		t.Fatal("plaintext key at rest")
	}
	if _, err = a.box.Open(encrypted, "ssh-key:"+cryptobox.ID()); err == nil {
		t.Fatal("ciphertext transplant accepted")
	}
	payload := map[string]any{"name": "VPS one", "hostname": "192.0.2.10", "ssh_port": 22, "ssh_user": "root", "host_key_fingerprint": k.Fingerprint, "ssh_auth_method": "saved_key", "ssh_key_id": k.ID}
	for _, name := range []string{"VPS one", "VPS two"} {
		payload["name"] = name
		if name == "VPS two" {
			payload["hostname"] = "192.0.2.20"
		}
		w := request(a, "POST", "/api/admin/servers", admin, post(payload))
		requireStatus(t, w, 201)
		var s store.Server
		json.Unmarshal(w.Body.Bytes(), &s)
		target, err := a.db.Target(ctx, s.ID)
		if err != nil {
			t.Fatal(err)
		}
		if target.SSHKeyID != k.ID || target.AuthMethod != "saved_key" {
			t.Fatal("key association lost")
		}
		remote := &credentialCheck{t: t, secret: private}
		worker := worker.Worker{Box: a.box, Remote: remote}
		if err = worker.Execute(ctx, store.Job{Kind: "check"}, target); err != nil || !remote.called {
			t.Fatalf("shared key not usable: %v", err)
		}
	}
	requireStatus(t, request(a, "DELETE", "/api/admin/ssh-keys/"+k.ID, admin, ""), 409)
	delete(payload, "ssh_key_id")
	payload["ssh_auth_method"] = "password"
	password := " test password with whitespace \n"
	payload["password"] = password
	payload["name"] = "Password VPS"
	payload["hostname"] = "192.0.2.30"
	w := request(a, "POST", "/api/admin/servers", admin, post(payload))
	requireStatus(t, w, 201)
	var s store.Server
	json.Unmarshal(w.Body.Bytes(), &s)
	target, err := a.db.Target(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := a.box.Open(target.EncryptedKey, "server:"+s.ID)
	if err != nil || string(plain) != password || bytes.Contains(target.EncryptedKey, []byte(password)) {
		t.Fatal("password encryption or whitespace failed")
	}
	remote := &credentialCheck{t: t, secret: []byte(password), method: "password"}
	runner := worker.Worker{Box: a.box, Remote: remote}
	if err = runner.Execute(ctx, store.Job{Kind: "check"}, target); err != nil || !remote.called {
		t.Fatalf("password not usable: %v", err)
	}
	for _, path := range []string{"/api/admin/servers", "/api/admin/ssh-keys", "/api/admin/events"} {
		r := request(a, "GET", path, admin, "")
		requireStatus(t, r, 200)
		if strings.Contains(r.Body.String(), "PRIVATE KEY") || strings.Contains(r.Body.String(), "encrypted_private_key") || strings.Contains(r.Body.String(), "test password") {
			t.Fatal("credential leaked")
		}
	}
	payload["password"] = ""
	requireStatus(t, request(a, "POST", "/api/admin/servers", admin, post(payload)), 400)
	payload["password"] = strings.Repeat("x", 4097)
	requireStatus(t, request(a, "POST", "/api/admin/servers", admin, post(payload)), 400)
	payload["password"] = "test"
	payload["private_key"] = "also supplied"
	requireStatus(t, request(a, "POST", "/api/admin/servers", admin, post(payload)), 400)
	delete(payload, "password")
	delete(payload, "private_key")
	payload["ssh_auth_method"] = "saved_key"
	payload["ssh_key_id"] = cryptobox.ID()
	payload["hostname"] = "192.0.2.40"
	requireStatus(t, request(a, "POST", "/api/admin/servers", admin, post(payload)), 400)
	payload["ssh_auth_method"] = "other"
	requireStatus(t, request(a, "POST", "/api/admin/servers", admin, post(payload)), 400)
	keys, err := a.db.SSHKeys(ctx)
	if err != nil || len(keys) != 1 || keys[0].ServerCount != 2 {
		t.Fatal("wrong shared key count")
	}
}
func TestImportAndDeleteUnusedSSHKey(t *testing.T) {
	a := testApp(t)
	admin := account(t, a, "admin")
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	block, _ := ssh.MarshalPrivateKey(key, "test")
	raw := pem.EncodeToMemory(block)
	body, _ := json.Marshal(map[string]string{"name": "Imported", "mode": "import", "private_key": string(raw)})
	w := request(a, "POST", "/api/admin/ssh-keys", admin, string(body))
	requireStatus(t, w, 201)
	var k store.SSHKey
	json.Unmarshal(w.Body.Bytes(), &k)
	var encrypted []byte
	if err := a.db.Pool.QueryRow(context.Background(), "SELECT encrypted_private_key FROM ssh_keys WHERE id=$1", k.ID).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	plain, err := a.box.Open(encrypted, "ssh-key:"+k.ID)
	if err != nil || !bytes.Equal(plain, raw) {
		t.Fatal("import corrupted")
	}
	requireStatus(t, request(a, "DELETE", "/api/admin/ssh-keys/"+k.ID, admin, ""), 204)
	requireStatus(t, request(a, "DELETE", "/api/admin/ssh-keys/"+k.ID, admin, ""), 404)
	requireStatus(t, request(a, "POST", "/api/admin/ssh-keys", admin, `{"name":"Invalid","mode":"import","private_key":"invalid"}`), 400)
}
