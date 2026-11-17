package httpapp

import (
	"encoding/json"
	"github.com/NetherQuartz/cu-chi/internal/store"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var countryCodes = func() map[string]bool {
	var rows []struct{ Code string }
	data, _ := web.ReadFile("web/countries.json")
	if err := json.Unmarshal(data, &rows); err != nil {
		panic(err)
	}
	out := map[string]bool{"": true}
	for _, r := range rows {
		out[r.Code] = true
	}
	return out
}()

func validLocation(country, city string) bool {
	return countryCodes[country] && utf8.ValidString(city) && utf8.RuneCountInString(city) <= 80 && !strings.ContainsFunc(city, unicode.IsControl)
}
func (a *App) monitoringRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PATCH /api/admin/servers/{id}/location", a.auth(true, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		id := r.PathValue("id")
		if !uuidRE.MatchString(id) {
			apiError(w, 404, "Сервер не найден")
			return
		}
		var v struct {
			Country string `json:"country_code"`
			City    string `json:"city"`
		}
		if !decode(w, r, &v) {
			return
		}
		v.City = strings.TrimSpace(v.City)
		if !validLocation(v.Country, v.City) {
			apiError(w, 400, "Выберите страну и укажите город до 80 символов")
			return
		}
		if err := a.db.SetLocation(r.Context(), s.User, id, v.Country, v.City); a.vpnError(w, err) {
			return
		}
		w.WriteHeader(204)
	}))
	mux.HandleFunc("GET /api/admin/monitoring", a.auth(true, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		v, err := a.db.Monitors(r.Context())
		if err != nil {
			a.failure(w, err)
			return
		}
		jsonResponse(w, 200, v)
	}))
	mux.HandleFunc("GET /api/admin/servers/{id}/history", a.auth(true, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		id := r.PathValue("id")
		if !uuidRE.MatchString(id) {
			apiError(w, 404, "Сервер не найден")
			return
		}
		hours := 1
		switch r.URL.Query().Get("period") {
		case "", "hour":
		case "day":
			hours = 24
		default:
			apiError(w, 400, "Выберите час или сутки")
			return
		}
		v, err := a.db.HostHistory(r.Context(), id, time.Now().Add(-time.Duration(hours)*time.Hour))
		if err != nil {
			a.failure(w, err)
			return
		}
		jsonResponse(w, 200, v)
	}))
	mux.HandleFunc("GET /api/services", a.auth(false, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		v, err := a.db.Services(r.Context(), s.User)
		if err != nil {
			a.failure(w, err)
			return
		}
		jsonResponse(w, 200, v)
	}))
	mux.HandleFunc("GET /api/traffic", a.auth(false, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		q := r.URL.Query()
		f := store.TrafficFilter{UserID: q.Get("user_id"), DeviceID: q.Get("device_id"), ServerID: q.Get("server_id"), Bucket: 60}
		hours := 24
		switch q.Get("period") {
		case "hour":
			hours = 1
		case "", "day":
			f.Bucket = 900
		case "week":
			hours = 168
			f.Bucket = 3600
		case "month":
			hours = 720
			f.Bucket = 86400
		default:
			apiError(w, 400, "Неизвестный период")
			return
		}
		for _, id := range []string{f.UserID, f.DeviceID, f.ServerID} {
			if id != "" && !uuidRE.MatchString(id) {
				apiError(w, 400, "Некорректный фильтр")
				return
			}
		}
		if s.User.Role != "admin" && f.UserID != "" && f.UserID != s.User.ID {
			apiError(w, 403, "Нет доступа")
			return
		}
		f.Since = time.Now().Add(-time.Duration(hours) * time.Hour)
		v, err := a.db.Traffic(r.Context(), s.User, f)
		if err != nil {
			a.failure(w, err)
			return
		}
		jsonResponse(w, 200, v)
	}))
	mux.Handle("GET /flags/{file}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file := r.PathValue("file")
		if len(file) != 6 || !strings.HasSuffix(file, ".svg") || !countryCodes[strings.ToUpper(file[:2])] {
			http.NotFound(w, r)
			return
		}
		b, err := web.ReadFile("web/flags/" + strings.ToLower(file))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Write(b)
	}))
}
