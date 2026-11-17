package httpapp

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/NetherQuartz/cu-chi/internal/store"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"

	"github.com/NetherQuartz/cu-chi/internal/cryptobox"
)

type loginFlow struct {
	Nonce    string `json:"nonce"`
	Verifier string `json:"verifier"`
}

func (a *App) cookie(w http.ResponseWriter, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, Secure: !a.cfg.DevAuth, SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
}
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	if a.oauth == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	state := cryptobox.Token()
	flow := loginFlow{Nonce: cryptobox.Token(), Verifier: oauth2.GenerateVerifier()}
	payload, _ := json.Marshal(flow)
	if err := a.db.SaveLogin(r.Context(), cryptobox.Hash(state), a.box.Seal(payload, "login:"+state)); err != nil {
		a.loginFailure(w, 503, "storage", "Сервис входа временно недоступен. Попробуйте ещё раз позже.", nil)
		return
	}
	a.cookie(w, "cuchi_login", state, 300)
	http.Redirect(w, r, a.oauth.AuthCodeURL(state, oidc.Nonce(flow.Nonce), oauth2.S256ChallengeOption(flow.Verifier)), http.StatusFound)
}
func (a *App) callback(w http.ResponseWriter, r *http.Request) {
	if a.oauth == nil {
		http.NotFound(w, r)
		return
	}
	c, err := r.Cookie("cuchi_login")
	state := r.URL.Query().Get("state")
	if err != nil || len(state) != 43 || subtle.ConstantTimeCompare([]byte(c.Value), []byte(state)) != 1 {
		a.loginFailure(w, 400, "state", "Запрос входа не совпадает с этой вкладкой или cookie входа уже отсутствует.", nil)
		return
	}
	a.cookie(w, "cuchi_login", "", -1)
	encrypted, err := a.db.ConsumeLogin(r.Context(), cryptobox.Hash(state))
	if err != nil {
		a.loginFailure(w, 400, "flow_consumed", "Запрос входа истёк или уже использован. Такое бывает при повторном открытии ссылки возврата.", nil)
		return
	}
	payload, err := a.box.Open(encrypted, "login:"+state)
	if err != nil {
		a.loginFailure(w, 400, "flow_invalid", "Не удалось проверить запрос входа.", nil)
		return
	}
	var flow loginFlow
	if json.Unmarshal(payload, &flow) != nil {
		a.loginFailure(w, 400, "flow_invalid", "Не удалось проверить запрос входа.", nil)
		return
	}
	if r.URL.Query().Get("error") != "" || r.URL.Query().Get("code") == "" {
		a.loginFailure(w, 400, "provider_cancelled", "Вход отменён или не подтверждён Synology.", nil)
		return
	}
	ctx := oidc.ClientContext(r.Context(), a.client)
	token, err := a.oauth.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(flow.Verifier))
	if err != nil {
		a.loginFailure(w, 502, "token_exchange", "Не удалось обменять код входа у Synology. Код мог истечь или уже использоваться; возможен также временный сбой соединения.", err)
		return
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		a.loginFailure(w, 502, "id_token_missing", "Synology не вернул подтверждение личности.", nil)
		return
	}
	id, err := a.verifier.Verify(ctx, raw)
	if err != nil || id.Subject == "" || subtle.ConstantTimeCompare([]byte(id.Nonce), []byte(flow.Nonce)) != 1 {
		a.loginFailure(w, 401, "id_token_invalid", "Ответ Synology не прошёл проверку. Вход не выполнен.", nil)
		return
	}
	var claims struct {
		Username string   `json:"username"`
		Groups   []string `json:"groups"`
	}
	if err := id.Claims(&claims); err != nil {
		a.loginFailure(w, 401, "claims_invalid", "Не удалось проверить группы пользователя Synology.", nil)
		return
	}
	role := roleFor(claims.Groups, a.cfg.AdminGroup, a.cfg.UserGroup)
	if claims.Username == "" {
		claims.Username = id.Subject
	}
	u, err := a.db.UpsertUser(r.Context(), id.Issuer, id.Subject, claims.Username, role)
	if err != nil {
		a.loginFailure(w, 503, "storage", "Сервис входа временно недоступен. Попробуйте ещё раз позже.", nil)
		return
	}
	if role == "denied" {
		a.loginFailure(w, 403, "access_denied", "У вашей учётной записи нет доступа. Обратитесь к администратору Synology.", nil)
		return
	}
	// ID-token exp bounds acceptance of the login assertion, not the RP session.
	// Signature, expiry, nonce and groups have already been verified above.
	expires := time.Now().Add(30 * time.Minute)
	value := cryptobox.Token()
	if err := a.db.CreateSession(r.Context(), cryptobox.Hash(value), u.ID, cryptobox.Token(), expires); err != nil {
		a.loginFailure(w, 503, "storage", "Сервис входа временно недоступен. Попробуйте ещё раз позже.", nil)
		return
	}
	// Rotate an existing browser session on reauthentication.
	if old, err := r.Cookie("cuchi_session"); err == nil {
		_ = a.db.DeleteSession(r.Context(), cryptobox.Hash(old.Value))
	}
	a.cookie(w, "cuchi_session", value, int(time.Until(expires).Seconds()))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func roleFor(groups []string, adminGroup, userGroup string) string {
	role := "denied"
	for _, group := range groups {
		if group == adminGroup {
			return "admin"
		}
		if group == userGroup {
			role = "user"
		}
	}
	return role
}
func (a *App) devLogin(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != a.cfg.PublicURL {
		apiError(w, 403, "Вход разрешён только с локальной страницы")
		return
	}
	u, err := a.db.UpsertUser(r.Context(), "urn:cuchi:development", "local-admin", "Разработчик", "admin")
	if err != nil {
		a.failure(w, err)
		return
	}
	if old, err := r.Cookie("cuchi_session"); err == nil {
		_ = a.db.DeleteSession(r.Context(), cryptobox.Hash(old.Value))
	}
	value := cryptobox.Token()
	if err := a.db.CreateSession(r.Context(), cryptobox.Hash(value), u.ID, cryptobox.Token(), time.Now().Add(30*time.Minute)); err != nil {
		a.failure(w, err)
		return
	}
	a.cookie(w, "cuchi_session", value, 1800)
	w.WriteHeader(204)
}

func (a *App) sessionActivity(w http.ResponseWriter, r *http.Request, _ store.Session) {
	cookie, _ := r.Cookie("cuchi_session")
	expires, err := a.db.RenewSession(r.Context(), cryptobox.Hash(cookie.Value))
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(w, 401, "Сессия завершена. Войдите снова")
		return
	}
	if err != nil {
		a.failure(w, err)
		return
	}
	a.cookie(w, "cuchi_session", cookie.Value, int(time.Until(expires).Seconds()))
	jsonResponse(w, 200, map[string]any{"expires_at": expires})
}
