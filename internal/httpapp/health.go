package httpapp

import (
	"github.com/NetherQuartz/cu-chi/internal/store"
	"net/http"
)

func (a *App) healthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/health", a.auth(false, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		v, err := a.db.Health(r.Context(), s.User)
		if err != nil {
			a.failure(w, err)
			return
		}
		jsonResponse(w, 200, v)
	}))
	mux.HandleFunc("POST /api/admin/servers/{id}/maintenance", a.auth(true, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		id := r.PathValue("id")
		if !uuidRE.MatchString(id) {
			apiError(w, 404, "Сервер не найден")
			return
		}
		var v struct {
			Minutes int `json:"minutes"`
		}
		if !decode(w, r, &v) {
			return
		}
		if v.Minutes < 0 || v.Minutes > 1440 {
			apiError(w, 400, "Укажите от 0 до 1440 минут")
			return
		}
		if a.vpnError(w, a.db.Maintenance(r.Context(), s.User, id, v.Minutes)) {
			return
		}
		w.WriteHeader(204)
	}))
	mux.HandleFunc("GET /api/notifications", a.auth(false, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		v, err := a.db.Inbox(r.Context(), s.User)
		if err != nil {
			a.failure(w, err)
			return
		}
		jsonResponse(w, 200, v)
	}))
	mux.HandleFunc("GET /api/notifications/unread-count", a.auth(false, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		count, err := a.db.UnreadCount(r.Context(), s.User)
		if err != nil {
			a.failure(w, err)
			return
		}
		jsonResponse(w, 200, map[string]int{"count": count})
	}))
	mux.HandleFunc("POST /api/notifications/read-all", a.auth(false, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		if err := a.db.ReadAllNotifications(r.Context(), s.User); err != nil {
			a.failure(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("POST /api/notifications/{id}/read", a.auth(false, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		id := r.PathValue("id")
		if !uuidRE.MatchString(id) {
			apiError(w, 404, "Уведомление не найдено")
			return
		}
		if a.vpnError(w, a.db.ReadNotification(r.Context(), s.User, id)) {
			return
		}
		w.WriteHeader(204)
	}))
	mux.HandleFunc("GET /api/admin/reminders", a.auth(true, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		v, err := a.db.ReminderAudience(r.Context())
		if err != nil {
			a.failure(w, err)
			return
		}
		jsonResponse(w, 200, v)
	}))
	mux.HandleFunc("POST /api/admin/reminders", a.auth(true, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		var input struct {
			RequestID string `json:"request_id"`
		}
		if !decode(w, r, &input) {
			return
		}
		if !uuidRE.MatchString(input.RequestID) {
			apiError(w, 400, "Обновите список получателей и повторите отправку")
			return
		}
		n, err := a.db.SendReminders(r.Context(), s.User, input.RequestID)
		if err != nil {
			a.failure(w, err)
			return
		}
		jsonResponse(w, 200, map[string]int64{"created": n})
	}))
}
