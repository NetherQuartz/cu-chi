package httpapp

import (
	"encoding/base64"
	"encoding/json"
	"github.com/NetherQuartz/cu-chi/internal/cryptobox"
	"github.com/NetherQuartz/cu-chi/internal/store"
	"github.com/NetherQuartz/cu-chi/internal/xray"
	qrcode "github.com/skip2/go-qrcode"
	"net/http"
	"strings"
)

func (a *App) xrayRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/xray", a.auth(false, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		nodes, e := a.db.XNodes(r.Context())
		if a.vpnError(w, e) {
			return
		}
		type service struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Applied int64  `json:"applied"`
		}
		ready := []service{}
		for _, n := range nodes {
			if n.Published && n.Applied > 0 {
				ready = append(ready, service{n.ID, n.Name, n.Applied})
			}
		}
		devices, e := a.db.XDevices(r.Context(), s.User)
		if a.vpnError(w, e) {
			return
		}
		history, e := a.db.ArchivedXServers(r.Context(), s.User)
		if a.vpnError(w, e) {
			return
		}
		jsonResponse(w, 200, map[string]any{"servers": ready, "devices": devices, "archived_servers": history})
	}))
	m.HandleFunc("GET /api/admin/xray", a.auth(true, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		n, e := a.db.XNodes(r.Context())
		if !a.vpnError(w, e) {
			jsonResponse(w, 200, n)
		}
	}))
	m.HandleFunc("POST /api/admin/servers/{id}/xray", a.auth(true, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		id := r.PathValue("id")
		if !uuidRE.MatchString(id) {
			apiError(w, 404, "Сервер не найден")
			return
		}
		options := struct {
			Published bool `json:"published"`
		}{Published: true}
		if r.ContentLength != 0 && !decode(w, r, &options) {
			return
		}
		k := xray.Generate()
		raw, _ := json.Marshal(k)
		e := a.db.XDeploy(r.Context(), s.User, id, a.box.Seal(raw, "xray-server:"+id), k.Public, k.Short, options.Published)
		if !a.vpnError(w, e) {
			w.WriteHeader(204)
		}
	}))
	m.HandleFunc("POST /api/admin/servers/{id}/xray-publication", a.auth(true, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		id := r.PathValue("id")
		if !uuidRE.MatchString(id) {
			apiError(w, 404, "Сервер не найден")
			return
		}
		var v struct {
			Published *bool `json:"published"`
		}
		if !decode(w, r, &v) {
			return
		}
		if v.Published == nil {
			apiError(w, 400, "Укажите режим публикации")
			return
		}
		if a.vpnError(w, a.db.XPublish(r.Context(), id, *v.Published)) {
			return
		}
		w.WriteHeader(204)
	}))
	for _, method := range []string{"POST", "DELETE"} {
		m.HandleFunc(method+" /api/devices/{id}/xray", a.auth(false, func(w http.ResponseWriter, r *http.Request, s store.Session) {
			id := r.PathValue("id")
			if !uuidRE.MatchString(id) {
				apiError(w, 404, "Устройство не найдено")
				return
			}
			seed := cryptobox.Token()
			hash := cryptobox.Hash(subscriptionToken([]byte(seed)))
			e := a.db.XChange(r.Context(), s.User, id, r.Method == "POST", a.box.Seal([]byte(seed), "xray-device:"+id), hash)
			if !a.vpnError(w, e) {
				w.WriteHeader(204)
			}
		}))
	}
	m.HandleFunc("GET /api/devices/{id}/xray/import", a.auth(false, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		d, e := a.db.XSecret(r.Context(), s.User, r.PathValue("id"), "")
		if a.vpnError(w, e) {
			return
		}
		raw, e := a.box.Open(d.Secret, "xray-device:"+d.ID)
		if a.vpnError(w, e) {
			return
		}
		defer clear(raw)
		sub := strings.TrimRight(a.cfg.PublicURL, "/") + "/sub/xray/" + subscriptionToken(raw)
		links, e := a.xrayLinks(r, d, raw)
		if a.vpnError(w, e) {
			return
		}
		if len(links) == 0 {
			apiError(w, 409, "Дождитесь применения профиля")
			return
		}
		if r.URL.Query().Get("qr") == "1" {
			png, e := qrcode.Encode(sub, qrcode.Medium, 384)
			if a.vpnError(w, e) {
				return
			}
			w.Header().Set("Content-Type", "image/png")
			w.Write(png)
			return
		}
		jsonResponse(w, 200, map[string]any{"subscription": sub, "links": links})
	}))
	m.HandleFunc("GET /sub/xray/{token}", func(w http.ResponseWriter, r *http.Request) {
		token := r.PathValue("token")
		if len(token) != 64 {
			http.NotFound(w, r)
			return
		}
		d, e := a.db.XSecret(r.Context(), store.User{}, "", cryptobox.Hash(token))
		if a.vpnError(w, e) {
			return
		}
		raw, e := a.box.Open(d.Secret, "xray-device:"+d.ID)
		if a.vpnError(w, e) {
			return
		}
		defer clear(raw)
		links, e := a.xrayLinks(r, d, raw)
		if a.vpnError(w, e) {
			return
		}
		if len(links) == 0 {
			apiError(w, 503, "Ожидается применение профиля")
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(strings.Join(links, "\n")))))
	})
}
func subscriptionToken(seed []byte) string { return cryptobox.Hash("subscription:" + string(seed)) }
func (a *App) xrayLinks(r *http.Request, d store.XDevice, seed []byte) ([]string, error) {
	nodes, e := a.db.XNodes(r.Context())
	if e != nil {
		return nil, e
	}
	var links []string
	for _, n := range nodes {
		if n.Published && n.Applied >= d.Revision {
			links = append(links, xray.Link(xray.ClientID(seed, n.ID), n.Host, n.SNI, n.Public, n.Short, n.Name))
		}
	}
	return links, nil
}
