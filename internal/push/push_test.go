package push

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	webpush "github.com/SherClockHolmes/webpush-go"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
)

func subscription() webpush.Subscription {
	k, _ := ecdh.P256().GenerateKey(rand.Reader)
	return webpush.Subscription{Endpoint: "https://fcm.googleapis.com/fcm/send/opaque", Keys: webpush.Keys{P256dh: base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()), Auth: base64.RawURLEncoding.EncodeToString(make([]byte, 16))}}
}
func TestSubscriptionSSRFValidation(t *testing.T) {
	s := subscription()
	if Validate(s) != nil {
		t.Fatal("valid rejected")
	}
	for _, endpoint := range []string{"http://fcm.googleapis.com/fcm/send/a", "https://fcm.googleapis.com.evil.example/a", "https://127.0.0.1/a", "https://user:secret@fcm.googleapis.com/a", "https://fcm.googleapis.com:444/a", "https://push.apple.com.evil.example/a", "https://fcm.googleapis.com/a#fragment", "https://evil.example/a", "https://fcm.googleapis.com./a"} {
		s.Endpoint = endpoint
		if Validate(s) == nil {
			t.Fatal("unsafe endpoint accepted", endpoint)
		}
	}
	s = subscription()
	s.Keys.P256dh = base64.RawURLEncoding.EncodeToString(make([]byte, 65))
	if Validate(s) == nil {
		t.Fatal("invalid curve point accepted")
	}
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "192.168.1.1", "169.254.169.254", "::1", "fc00::1", "::ffff:127.0.0.1", "100.64.0.1", "198.18.0.1", "64:ff9b::7f00:1", "2001:db8::1"} {
		if publicIP(net.ParseIP(ip)) {
			t.Fatal("private/reserved accepted", ip)
		}
	}
	if !publicIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("public denied")
	}
	if Client().CheckRedirect(nil, nil) == nil {
		t.Fatal("redirect allowed")
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestEncryptedUrgentDelivery(t *testing.T) {
	priv, pub, _ := webpush.GenerateVAPIDKeys()
	s := subscription()
	called := false
	c := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		if r.Header.Get("Urgency") != "high" || r.Header.Get("TTL") != "60" || r.Header.Get("Authorization") == "" {
			t.Fatal("push headers")
		}
		b, _ := io.ReadAll(r.Body)
		if strings.Contains(string(b), "Критично") || strings.Contains(string(b), "Подробности") {
			t.Fatal("unencrypted payload")
		}
		return &http.Response{StatusCode: 201, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	code, err := Send(context.Background(), c, s, priv, pub, "https://portal.example", "11111111-1111-1111-1111-111111111111", "outage", "critical", 60)
	if err != nil || code != 201 || !called {
		t.Fatal(code, err)
	}
}

func TestOperationPayloadContext(t *testing.T) {
	details := Context{ActorNames: "alice\nbob", ServerName: "Replacement"}
	p := string(Payload("id", "job_failed", "warning", details))
	if !strings.Contains(p, "alice bob") || !strings.Contains(p, "Replacement") {
		t.Fatal("missing operation context", p)
	}
	p = string(Payload("id", "reminder", "warning", details))
	if strings.Contains(p, "alice") || strings.Contains(p, "Replacement") {
		t.Fatal("operation context exposed in ordinary notice", p)
	}
	if len([]rune(pushLabel(strings.Repeat("я", 500), 100))) != 101 {
		t.Fatal("label limit")
	}
}
