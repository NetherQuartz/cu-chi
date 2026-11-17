package httpapp

import (
	"encoding/json"
	"github.com/NetherQuartz/cu-chi/internal/cryptobox"
	"github.com/NetherQuartz/cu-chi/internal/push"
	"github.com/NetherQuartz/cu-chi/internal/store"
	webpush "github.com/SherClockHolmes/webpush-go"
	"net/http"
)

func (a *App) pushRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/push", a.auth(false, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		private, public, err := webpush.GenerateVAPIDKeys()
		if err != nil {
			a.failure(w, err)
			return
		}
		public, _, err = a.db.PushIdentity(r.Context(), public, a.box.Seal([]byte(private), "push:vapid"))
		if err != nil {
			a.failure(w, err)
			return
		}
		prefs, err := a.db.PushPreferences(r.Context(), s.User)
		if err != nil {
			a.failure(w, err)
			return
		}
		jsonResponse(w, 200, map[string]any{"public_key": public, "preferences": prefs})
	}))
	mux.HandleFunc("PUT /api/push/preferences", a.auth(false, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		var p store.PushPreferences
		if !decode(w, r, &p) {
			return
		}
		if err := a.db.SavePushPreferences(r.Context(), s.User, p); err != nil {
			a.failure(w, err)
			return
		}
		w.WriteHeader(204)
	}))
	mux.HandleFunc("POST /api/push/subscription", a.auth(false, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		// expirationTime is a browser advisory, not an authorization boundary.
		var v struct {
			Endpoint       string       `json:"endpoint"`
			ExpirationTime *float64     `json:"expirationTime"`
			Keys           webpush.Keys `json:"keys"`
		}
		if !decode(w, r, &v) {
			return
		}
		sub := webpush.Subscription{Endpoint: v.Endpoint, Keys: v.Keys}
		if push.Validate(sub) != nil {
			apiError(w, 400, "Браузер передал неподдерживаемую push-подписку")
			return
		}
		raw, _ := json.Marshal(sub)
		defer clear(raw)
		err := a.db.SaveSubscription(r.Context(), s.User, cryptobox.Hash(sub.Endpoint), func(id string) []byte { return a.box.Seal(raw, "push-subscription:"+id) })
		if a.vpnError(w, err) {
			return
		}
		w.WriteHeader(204)
	}))
	mux.HandleFunc("DELETE /api/push/subscription", a.auth(false, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		var v struct {
			Endpoint string `json:"endpoint"`
		}
		if !decode(w, r, &v) {
			return
		}
		if err := a.db.RemoveSubscription(r.Context(), s.User, cryptobox.Hash(v.Endpoint)); err != nil {
			a.failure(w, err)
			return
		}
		w.WriteHeader(204)
	}))
	mux.HandleFunc("POST /api/push/status", a.auth(false, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		var v struct {
			Endpoint string `json:"endpoint"`
		}
		if !decode(w, r, &v) {
			return
		}
		status, err := a.db.PushStatus(r.Context(), s.User, cryptobox.Hash(v.Endpoint))
		if err != nil {
			a.failure(w, err)
			return
		}
		jsonResponse(w, 200, status)
	}))
	mux.HandleFunc("POST /api/push/test", a.auth(false, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		var v struct {
			Endpoint string `json:"endpoint"`
		}
		if !decode(w, r, &v) {
			return
		}
		if a.vpnError(w, a.db.TestPush(r.Context(), s.User, cryptobox.Hash(v.Endpoint))) {
			return
		}
		jsonResponse(w, 202, map[string]string{"status": "accepted"})
	}))
}
