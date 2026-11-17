package httpapp

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

func TestTokenAuthStyleUsesDiscovery(t *testing.T) {
	for _, tc := range []struct {
		methods []string
		want    oauth2.AuthStyle
		fail    bool
	}{{nil, oauth2.AuthStyleInHeader, false}, {[]string{"client_secret_post", "client_secret_basic"}, oauth2.AuthStyleInHeader, false}, {[]string{"client_secret_post"}, oauth2.AuthStyleInParams, false}, {[]string{"private_key_jwt"}, 0, true}} {
		got, err := tokenAuthStyle(tc.methods)
		if got != tc.want || (err != nil) != tc.fail {
			t.Fatalf("%v: %v %v", tc.methods, got, err)
		}
	}
}
func TestLoginErrorDoesNotExposeProviderSecrets(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(old)
	a := &App{}
	w := httptest.NewRecorder()
	sensitive := "SECRET-token-code-description"
	a.loginFailure(w, 502, "token_exchange", "Временная ошибка входа.", &oauth2.RetrieveError{Response: &http.Response{StatusCode: 400}, Body: []byte(sensitive), ErrorCode: "invalid_grant", ErrorDescription: sensitive})
	if w.Code != 502 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Fatal("browser received a non-HTML error")
	}
	for _, want := range []string{"Повторить вход", "Открыть кабинет", "name=\"viewport\""} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatal("missing recovery UI: " + want)
		}
	}
	if strings.Contains(logs.String(), sensitive) || strings.Contains(w.Body.String(), sensitive) {
		t.Fatal("provider details leaked")
	}
	if !strings.Contains(logs.String(), "invalid_grant") || !strings.Contains(logs.String(), "upstream_status=400") {
		t.Fatal("safe diagnosis missing")
	}
	for _, err := range []error{errors.New(sensitive), &oauth2.RetrieveError{ErrorCode: sensitive, ErrorDescription: sensitive}} {
		category, _ := loginErrorCategory(err)
		if strings.Contains(category, sensitive) {
			t.Fatal("unsanitized category")
		}
	}
}
