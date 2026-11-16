package config

import (
	"encoding/base64"
	"testing"
)

func TestDevelopmentAuthenticationCannotBindPublicly(t *testing.T) {
	for _, test := range []struct {
		name, listen, origin string
		valid                bool
	}{
		{"loopback", "127.0.0.1:8080", "http://127.0.0.1:8080", true},
		{"public bind", "0.0.0.0:8080", "http://127.0.0.1:8080", false},
		{"public URL", "127.0.0.1:8080", "https://portal.example.test", false},
		{"different port", "127.0.0.1:8080", "http://127.0.0.1:9090", false},
		{"path", "127.0.0.1:8080", "http://127.0.0.1:8080/path", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("CUCHI_MASTER_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
			t.Setenv("CUCHI_DATABASE_URL", "postgres://test")
			t.Setenv("CUCHI_DEV_AUTH", "true")
			t.Setenv("CUCHI_LISTEN", test.listen)
			t.Setenv("CUCHI_PUBLIC_URL", test.origin)
			_, err := Load()
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v, error=%v", test.valid, err)
			}
		})
	}
}
func TestProductionRequiresOIDCAndHTTPS(t *testing.T) {
	t.Setenv("CUCHI_MASTER_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("CUCHI_DATABASE_URL", "postgres://test")
	t.Setenv("CUCHI_DEV_AUTH", "false")
	t.Setenv("CUCHI_LISTEN", ":8080")
	t.Setenv("CUCHI_PUBLIC_URL", "https://portal.example.test")
	t.Setenv("CUCHI_OIDC_ISSUER", "https://sso.example.test/webman/sso")
	t.Setenv("CUCHI_OIDC_CLIENT_ID", "test")
	t.Setenv("CUCHI_OIDC_CLIENT_SECRET", "test")
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CUCHI_PUBLIC_URL", "http://portal.example.test")
	if _, err := Load(); err == nil {
		t.Fatal("HTTP production accepted")
	}
	t.Setenv("CUCHI_PUBLIC_URL", "https://portal.example.test")
	t.Setenv("CUCHI_OIDC_CLIENT_SECRET", "")
	if _, err := Load(); err == nil {
		t.Fatal("missing OIDC secret accepted")
	}
}
