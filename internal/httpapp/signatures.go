package httpapp

import (
	"net/http"

	"github.com/NetherQuartz/cu-chi/internal/awg"
	"github.com/NetherQuartz/cu-chi/internal/store"
)

func (a *App) signatureRoutes(m *http.ServeMux) {
	m.HandleFunc("PUT /api/admin/servers/{id}/signature", a.auth(true, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		id := r.PathValue("id")
		if !uuidRE.MatchString(id) {
			apiError(w, 404, "Сервер не найден")
			return
		}
		var v struct {
			Mode     string `json:"mode"`
			Custom   string `json:"custom"`
			Revision int64  `json:"revision"`
		}
		if !decode(w, r, &v) {
			return
		}
		var signature string
		switch v.Mode {
		case "off":
		case "amnezia":
			signature = awg.DefaultSignature()
		case "custom":
			if !awg.ValidSignature(v.Custom) {
				apiError(w, 400, "Проверьте шаблон I1: допустимы теги b, r, rd, rc и t, до 1500 символов и 1400 байт в пакете")
				return
			}
			signature = v.Custom
		default:
			apiError(w, 400, "Выберите вариант сигнатуры")
			return
		}
		if v.Revision < 1 {
			apiError(w, 400, "Нужна версия настроек")
			return
		}
		if !a.vpnError(w, a.db.SetSignature(r.Context(), s.User, id, signature, v.Revision)) {
			w.WriteHeader(204)
		}
	}))
	m.HandleFunc("POST /api/profiles/{id}/confirm-import", a.auth(false, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		id := r.PathValue("id")
		if !uuidRE.MatchString(id) {
			apiError(w, 404, "Профиль не найден")
			return
		}
		var v struct {
			Revision int64 `json:"revision"`
		}
		if !decode(w, r, &v) {
			return
		}
		if !a.vpnError(w, a.db.ConfirmProfileImport(r.Context(), s.User, id, v.Revision)) {
			w.WriteHeader(204)
		}
	}))
}
