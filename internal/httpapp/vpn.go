package httpapp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/NetherQuartz/cu-chi/internal/awg"
	"github.com/NetherQuartz/cu-chi/internal/cryptobox"
	"github.com/NetherQuartz/cu-chi/internal/deployer"
	"github.com/NetherQuartz/cu-chi/internal/store"
	qrcode "github.com/skip2/go-qrcode"
)

func (a *App) vpnRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/profiles", a.auth(false, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		v, err := a.db.Profiles(r.Context(), s.User)
		if err != nil {
			a.failure(w, err)
			return
		}
		jsonResponse(w, 200, v)
	}))
	mux.HandleFunc("GET /api/vpn", a.auth(false, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		servers, err := a.db.Servers(r.Context())
		if err != nil {
			a.failure(w, err)
			return
		}
		v := map[string]any{"available": false}
		for _, server := range servers {
			if server.Default {
				v["server_name"] = server.Name
				v["server_id"] = server.ID
				v["available"] = server.VPNState == "ready"
			}
		}
		jsonResponse(w, 200, v)
	}))
	mux.HandleFunc("POST /api/devices/{id}/profiles", a.auth(false, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		id := r.PathValue("id")
		if !uuidRE.MatchString(id) {
			apiError(w, 404, "Устройство не найдено")
			return
		}
		k := awg.NewKeys()
		p := store.Profile{ID: cryptobox.ID(), DeviceID: id, PublicKey: k.Public}
		p.EncryptedKeys = a.box.Seal(awg.Marshal(k), "profile:"+p.ID)
		p, err := a.db.Issue(r.Context(), s.User, p)
		if a.vpnError(w, err) {
			return
		}
		jsonResponse(w, 202, p)
	}))
	mux.HandleFunc("POST /api/devices/{id}/revoke", a.auth(false, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		id := r.PathValue("id")
		if !uuidRE.MatchString(id) {
			apiError(w, 404, "Устройство не найдено")
			return
		}
		if a.vpnError(w, a.db.Revoke(r.Context(), s.User, id)) {
			return
		}
		jsonResponse(w, 202, map[string]string{"status": "revoking"})
	}))
	mux.HandleFunc("GET /api/profiles/{id}/download", a.auth(false, a.downloadProfile))
	mux.HandleFunc("GET /api/profiles/{id}/qr", a.auth(false, a.downloadProfile))
	mux.HandleFunc("GET /api/admin/jobs", a.auth(true, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		v, err := a.db.Jobs(r.Context())
		if err != nil {
			a.failure(w, err)
			return
		}
		jsonResponse(w, 200, v)
	}))
	mux.HandleFunc("POST /api/admin/servers/{id}/check", a.auth(true, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		id := r.PathValue("id")
		if !uuidRE.MatchString(id) {
			apiError(w, 404, "Сервер не найден")
			return
		}
		j, err := a.db.QueueServer(r.Context(), s.User, id, "check", "", 0, nil)
		if a.vpnError(w, err) {
			return
		}
		jsonResponse(w, 202, map[string]string{"job_id": j})
	}))
	mux.HandleFunc("POST /api/admin/servers/{id}/deploy", a.auth(true, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		id := r.PathValue("id")
		if !uuidRE.MatchString(id) {
			apiError(w, 404, "Сервер не найден")
			return
		}
		var v struct {
			Endpoint string `json:"endpoint"`
			Port     int    `json:"port"`
		}
		if !decode(w, r, &v) {
			return
		}
		if v.Port < 1 || v.Port > 65535 || (net.ParseIP(v.Endpoint) == nil && !hostnameRE.MatchString(v.Endpoint)) {
			apiError(w, 400, "Проверьте публичный адрес и UDP-порт")
			return
		}
		if !deployer.ValidImage(a.cfg.AWGImage) {
			apiError(w, 503, "Образ AmneziaWG ещё не настроен. Обновите панель из GitHub CI")
			return
		}
		settings := awg.NewSettings()
		j, err := a.db.QueueServer(r.Context(), s.User, id, "deploy", v.Endpoint, v.Port, a.box.Seal(awg.Marshal(settings), "vpn:"+id))
		if a.vpnError(w, err) {
			return
		}
		jsonResponse(w, 202, map[string]string{"job_id": j})
	}))
	mux.HandleFunc("POST /api/admin/servers/{id}/default", a.auth(true, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		id := r.PathValue("id")
		if !uuidRE.MatchString(id) {
			apiError(w, 404, "Сервер не найден")
			return
		}
		if a.vpnError(w, a.db.SetDefaultServer(r.Context(), s.User, id)) {
			return
		}
		w.WriteHeader(204)
	}))
}
func (a *App) vpnError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, 404, "Не найдено")
	} else if errors.Is(err, store.ErrConflict) {
		apiError(w, 409, "Операция пока недоступна: дождитесь применения профиля, выберите готовый сервер или завершите отзыв. Адрес и порт развёрнутого сервера менять нельзя.")
	} else {
		a.failure(w, err)
	}
	return true
}
func (a *App) downloadProfile(w http.ResponseWriter, r *http.Request, session store.Session) {
	id := r.PathValue("id")
	if !uuidRE.MatchString(id) {
		apiError(w, 404, "Профиль не найден")
		return
	}
	p, t, err := a.db.Download(r.Context(), session.User, id)
	if a.vpnError(w, err) {
		return
	}
	raw, err := a.box.Open(p.EncryptedKeys, "profile:"+p.ID)
	if err != nil {
		a.failure(w, errors.New("profile decryption failed"))
		return
	}
	defer clear(raw)
	cfg, err := a.box.Open(t.EncryptedSettings, "vpn:"+t.ID)
	if err != nil {
		a.failure(w, errors.New("VPN settings decryption failed"))
		return
	}
	defer clear(cfg)
	var k awg.Keys
	var s awg.Settings
	if json.Unmarshal(raw, &k) != nil || json.Unmarshal(cfg, &s) != nil {
		a.failure(w, errors.New("invalid stored profile"))
		return
	}
	profile := s.Client(k, p.Address, t.Endpoint, t.VPNPort)
	if r.Pattern == "GET /api/profiles/{id}/qr" {
		png, err := qrcode.Encode(profile, qrcode.Medium, 512)
		if err != nil {
			a.failure(w, errors.New("QR generation failed"))
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(png)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="cuchi-%s.conf"`, p.ID))
	w.Write([]byte(profile))
}
