package httpapp

import (
	"errors"
	"html/template"
	"log/slog"
	"net"
	"net/http"

	"github.com/NetherQuartz/cu-chi/internal/cryptobox"
	"golang.org/x/oauth2"
)

var loginErrorPage = template.Must(template.New("login-error").Parse(`<!doctype html>
<html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Не удалось войти · Củ Chi</title><link rel="icon" href="/icon.svg" type="image/svg+xml"><link rel="stylesheet" href="/app.css"></head>
<body><main class="auth-error"><a class="brand" href="/"><img src="/icon.svg" width="35" height="35" alt=""><span>Củ Chi</span></a><p class="eyebrow">ВХОД ЧЕРЕЗ SYNOLOGY</p><h1>Не удалось завершить вход</h1><p class="muted">{{.Message}}</p><p>Нажмите «Повторить вход», чтобы начать заново. Если вы уже вошли в другой вкладке, откройте кабинет.</p><div class="card-actions"><a class="button primary" href="/auth/login">Повторить вход</a><a class="button secondary" href="/">Открыть кабинет</a></div><p class="small muted">Если ошибка повторяется, сообщите администратору этот номер:</p><code class="error-reference">{{.Reference}}</code></main></body></html>`))

// Never log provider response bodies, descriptions, codes, tokens or callback URLs.
func (a *App) loginFailure(w http.ResponseWriter, status int, stage, message string, err error) {
	ref := cryptobox.ID()
	category, upstream := loginErrorCategory(err)
	slog.Warn("OIDC login failed", "reference", ref, "stage", stage, "status", status, "category", category, "upstream_status", upstream)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = loginErrorPage.Execute(w, struct{ Message, Reference string }{message, ref})
}
func loginErrorCategory(err error) (string, int) {
	if err == nil {
		return "rejected", 0
	}
	var retrieve *oauth2.RetrieveError
	if errors.As(err, &retrieve) {
		status := 0
		if retrieve.Response != nil {
			status = retrieve.Response.StatusCode
		}
		switch retrieve.ErrorCode {
		case "invalid_request", "invalid_client", "invalid_grant", "unauthorized_client", "unsupported_grant_type", "invalid_scope", "server_error", "temporarily_unavailable":
			return retrieve.ErrorCode, status
		}
		return "token_response", status
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return "timeout", 0
	}
	return "transport_or_validation", 0
}

// Discovery selects one supported client authentication method. Automatic probing
// retries an already-used authorization code after ANY exchange error.
func tokenAuthStyle(methods []string) (oauth2.AuthStyle, error) {
	if len(methods) == 0 {
		return oauth2.AuthStyleInHeader, nil
	}
	for _, method := range methods {
		if method == "client_secret_basic" {
			return oauth2.AuthStyleInHeader, nil
		}
	}
	for _, method := range methods {
		if method == "client_secret_post" {
			return oauth2.AuthStyleInParams, nil
		}
	}
	return 0, errors.New("OIDC provider has no supported client-secret authentication method")
}
