package httpapp

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
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
		a.failure(w, err)
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
		apiError(w, 400, "Не удалось подтвердить вход. Начните заново")
		return
	}
	a.cookie(w, "cuchi_login", "", -1)
	encrypted, err := a.db.ConsumeLogin(r.Context(), cryptobox.Hash(state))
	if err != nil {
		apiError(w, 400, "Запрос входа истёк или уже использован")
		return
	}
	payload, err := a.box.Open(encrypted, "login:"+state)
	if err != nil {
		apiError(w, 400, "Не удалось подтвердить вход")
		return
	}
	var flow loginFlow
	if json.Unmarshal(payload, &flow) != nil {
		apiError(w, 400, "Не удалось подтвердить вход")
		return
	}
	if r.URL.Query().Get("error") != "" || r.URL.Query().Get("code") == "" {
		apiError(w, 400, "Вход отменён или не подтверждён Synology")
		return
	}
	ctx := oidc.ClientContext(r.Context(), a.client)
	token, err := a.oauth.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(flow.Verifier))
	if err != nil {
		apiError(w, 502, "Synology не подтвердил код входа")
		return
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		apiError(w, 502, "Synology не вернул ID token")
		return
	}
	id, err := a.verifier.Verify(ctx, raw)
	if err != nil || id.Subject == "" || subtle.ConstantTimeCompare([]byte(id.Nonce), []byte(flow.Nonce)) != 1 {
		apiError(w, 401, "Ответ Synology не прошёл проверку")
		return
	}
	var claims struct {
		Username string   `json:"username"`
		Groups   []string `json:"groups"`
	}
	if err := id.Claims(&claims); err != nil {
		apiError(w, 401, "Некорректные группы пользователя")
		return
	}
	role := roleFor(claims.Groups, a.cfg.AdminGroup, a.cfg.UserGroup)
	if claims.Username == "" {
		claims.Username = id.Subject
	}
	u, err := a.db.UpsertUser(r.Context(), id.Issuer, id.Subject, claims.Username, role)
	if err != nil {
		a.failure(w, err)
		return
	}
	if role == "denied" {
		apiError(w, 403, "Доступ не разрешён. Обратитесь к администратору Synology")
		return
	}
	expires := time.Now().Add(30 * time.Minute)
	if id.Expiry.Before(expires) {
		expires = id.Expiry
	}
	value := cryptobox.Token()
	if err := a.db.CreateSession(r.Context(), cryptobox.Hash(value), u.ID, cryptobox.Token(), expires); err != nil {
		a.failure(w, err)
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
