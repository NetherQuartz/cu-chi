package httpapp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/NetherQuartz/cu-chi/internal/deployer"
	"github.com/NetherQuartz/cu-chi/internal/gateway"
	"github.com/NetherQuartz/cu-chi/internal/store"
)

func awgMarshalLAN(lan gateway.LAN) []byte { b, _ := json.Marshal(lan); return b }
func (a *App) gatewayRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/admin/gateways", a.auth(true, a.createServer))
	mux.HandleFunc("GET /api/admin/gateways", a.auth(true, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		v, err := a.db.Gateways(r.Context())
		if err != nil {
			a.failure(w, err)
			return
		}
		jsonResponse(w, 200, v)
	}))
	mux.HandleFunc("POST /api/admin/gateways/{id}/{action}", a.auth(true, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		id, action := r.PathValue("id"), r.PathValue("action")
		if !uuidRE.MatchString(id) || (action != "check" && action != "deploy") {
			apiError(w, 404, "Не найдено")
			return
		}
		if action == "deploy" && !deployer.ValidGatewayImage(a.cfg.GatewayImage) {
			apiError(w, 503, "Обновите панель из GitHub CI: образ шлюза не настроен")
			return
		}
		err := a.db.QueueGateway(r.Context(), s.User, id, action)
		if errors.Is(err, store.ErrConflict) {
			apiError(w, 409, "Дождитесь текущей операции. Для развёртывания сначала выпустите профиль шлюза и дождитесь его готовности на VPS.")
			return
		}
		if a.vpnError(w, err) {
			return
		}
		jsonResponse(w, 202, map[string]string{"status": "queued"})
	}))
	mux.HandleFunc("GET /api/admin/gateways/{id}/keenetic.conf", a.auth(true, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		id := r.PathValue("id")
		if !uuidRE.MatchString(id) {
			apiError(w, 404, "Не найдено")
			return
		}
		g, err := a.db.Gateway(r.Context(), id)
		if a.vpnError(w, err) {
			return
		}
		raw, err := a.box.Open(g.EncryptedLAN, "gateway-lan:"+id)
		if err != nil {
			a.failure(w, errors.New("LAN identity decryption failed"))
			return
		}
		defer clear(raw)
		var lan gateway.LAN
		if json.Unmarshal(raw, &lan) != nil {
			a.failure(w, errors.New("invalid LAN identity"))
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="cuchi-keenetic-%s.conf"`, id))
		w.Write([]byte(lan.RouterConfig))
	}))
}
