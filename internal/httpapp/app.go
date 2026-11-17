package httpapp

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/ssh"
	"golang.org/x/oauth2"

	"github.com/NetherQuartz/cu-chi/internal/config"
	"github.com/NetherQuartz/cu-chi/internal/cryptobox"
	"github.com/NetherQuartz/cu-chi/internal/deployer"
	"github.com/NetherQuartz/cu-chi/internal/store"
)

//go:embed web/*
var web embed.FS

type App struct {
	cfg      config.Config
	db       *store.Store
	box      *cryptobox.Box
	oauth    *oauth2.Config
	verifier *oidc.IDTokenVerifier
	client   *http.Client
}
type authenticated func(http.ResponseWriter, *http.Request, store.Session)

func New(ctx context.Context, cfg config.Config, db *store.Store) (*App, error) {
	box, err := cryptobox.New(cfg.MasterKey)
	if err != nil {
		return nil, err
	}
	a := &App{cfg: cfg, db: db, box: box, client: &http.Client{Timeout: 15 * time.Second}}
	if !cfg.DevAuth {
		provider, err := oidc.NewProvider(oidc.ClientContext(ctx, a.client), cfg.Issuer)
		if err != nil {
			return nil, errors.New("OIDC discovery failed; check issuer, TLS and network access")
		}
		var metadata struct {
			TokenAuthMethods []string `json:"token_endpoint_auth_methods_supported"`
		}
		if err = provider.Claims(&metadata); err != nil {
			return nil, errors.New("invalid OIDC discovery metadata")
		}
		endpoint := provider.Endpoint()
		endpoint.AuthStyle, err = tokenAuthStyle(metadata.TokenAuthMethods)
		if err != nil {
			return nil, err
		}
		a.oauth = &oauth2.Config{ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret, Endpoint: endpoint, RedirectURL: cfg.PublicURL + "/auth/callback", Scopes: []string{oidc.ScopeOpenID, "groups"}}
		a.verifier = provider.Verifier(&oidc.Config{ClientID: cfg.ClientID})
	}
	return a, nil
}
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	a.vpnRoutes(mux)
	a.sshKeyRoutes(mux)
	a.monitoringRoutes(mux)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { jsonResponse(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := a.db.Pool.Ping(ctx); err != nil {
			apiError(w, 503, "База данных недоступна")
			return
		}
		jsonResponse(w, 200, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("GET /api/public", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, 200, map[string]any{"dev_auth": a.cfg.DevAuth})
	})
	mux.HandleFunc("GET /auth/login", a.login)
	mux.HandleFunc("GET /auth/callback", a.callback)
	if a.cfg.DevAuth {
		mux.HandleFunc("POST /auth/dev", a.devLogin)
	}
	mux.HandleFunc("GET /api/me", a.auth(false, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		jsonResponse(w, 200, map[string]any{"user": s.User, "csrf_token": s.CSRF, "expires_at": s.Expires, "dev_auth": a.cfg.DevAuth})
	}))
	mux.HandleFunc("POST /api/logout", a.auth(false, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		c, _ := r.Cookie("cuchi_session")
		if err := a.db.DeleteSession(r.Context(), cryptobox.Hash(c.Value)); err != nil {
			a.failure(w, err)
			return
		}
		a.cookie(w, "cuchi_session", "", -1)
		w.WriteHeader(204)
	}))
	mux.HandleFunc("GET /api/devices", a.auth(false, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		v, err := a.db.Devices(r.Context(), s.User.ID, s.User.Role == "admin")
		if err != nil {
			a.failure(w, err)
			return
		}
		jsonResponse(w, 200, v)
	}))
	mux.HandleFunc("POST /api/devices", a.auth(false, a.createDevice))
	mux.HandleFunc("DELETE /api/devices/{id}", a.auth(false, a.deleteDevice))
	mux.HandleFunc("GET /api/admin/servers", a.auth(true, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		v, err := a.db.Servers(r.Context())
		if err != nil {
			a.failure(w, err)
			return
		}
		jsonResponse(w, 200, v)
	}))
	mux.HandleFunc("POST /api/admin/servers", a.auth(true, a.createServer))
	mux.HandleFunc("GET /api/admin/events", a.auth(true, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		v, err := a.db.Events(r.Context())
		if err != nil {
			a.failure(w, err)
			return
		}
		jsonResponse(w, 200, v)
	}))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { apiError(w, 404, "Не найдено") })
	assets, _ := fs.Sub(web, "web")
	for _, path := range []string{"/{$}", "/app.js", "/monitoring.js", "/countries.json", "/app.css", "/icon.svg", "/platform-ios.svg", "/platform-android.svg", "/platform-windows.svg", "/platform-macos.svg", "/platform-linux.svg", "/platform-gateway.svg"} {
		mux.Handle("GET "+path, http.FileServerFS(assets))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		w.Header().Set("Cache-Control", "no-store")
		if !a.cfg.DevAuth {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		mux.ServeHTTP(w, r)
	})
}
func (a *App) auth(admin bool, next authenticated) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("cuchi_session")
		if err != nil || len(c.Value) != 43 {
			apiError(w, 401, "Войдите в аккаунт")
			return
		}
		s, err := a.db.Session(r.Context(), cryptobox.Hash(c.Value))
		if errors.Is(err, pgx.ErrNoRows) {
			apiError(w, 401, "Сессия завершена. Войдите снова")
			return
		}
		if err != nil {
			a.failure(w, err)
			return
		}
		if admin && s.User.Role != "admin" {
			apiError(w, 403, "Нужны права администратора")
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			if r.Header.Get("Origin") != a.cfg.PublicURL || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(s.CSRF)) != 1 {
				apiError(w, 403, "Запрос не прошёл проверку безопасности. Обновите страницу")
				return
			}
		}
		next(w, r, s)
	}
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		apiError(w, 415, "Ожидается application/json")
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		apiError(w, 400, "Некорректный запрос")
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		apiError(w, 400, "Некорректный запрос")
		return false
	}
	return true
}
func (a *App) createDevice(w http.ResponseWriter, r *http.Request, s store.Session) {
	var v struct {
		Name     string `json:"name"`
		Platform string `json:"platform"`
	}
	if !decode(w, r, &v) {
		return
	}
	v.Name = strings.TrimSpace(v.Name)
	if !validName(v.Name) {
		apiError(w, 400, "Название должно содержать от 1 до 80 символов")
		return
	}
	switch v.Platform {
	case "ios", "android", "windows", "macos", "linux":
	case "gateway":
		if s.User.Role != "admin" {
			apiError(w, 403, "Шлюзы добавляет администратор")
			return
		}
	default:
		apiError(w, 400, "Выберите платформу")
		return
	}
	d, err := a.db.CreateDevice(r.Context(), s.User, v.Name, v.Platform)
	if err != nil {
		a.failure(w, err)
		return
	}
	jsonResponse(w, 201, d)
}

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (a *App) deleteDevice(w http.ResponseWriter, r *http.Request, s store.Session) {
	id := r.PathValue("id")
	if !uuidRE.MatchString(id) {
		apiError(w, 404, "Устройство не найдено")
		return
	}
	err := a.db.DeleteDevice(r.Context(), s.User, id)
	if errors.Is(err, store.ErrConflict) {
		apiError(w, 409, "Сначала отзовите все профили и дождитесь подтверждения серверов")
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, 404, "Устройство не найдено")
		return
	}
	if err != nil {
		a.failure(w, err)
		return
	}
	w.WriteHeader(204)
}

var userRE = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]{0,63}$`)
var hostnameRE = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9.-]{0,251}[a-zA-Z0-9])?$`)

func (a *App) createServer(w http.ResponseWriter, r *http.Request, s store.Session) {
	var input struct {
		CountryCode   string `json:"country_code"`
		City          string `json:"city"`
		Name          string `json:"name"`
		Hostname      string `json:"hostname"`
		SSHPort       int    `json:"ssh_port"`
		SSHUser       string `json:"ssh_user"`
		Fingerprint   string `json:"host_key_fingerprint"`
		HostKeyPolicy string `json:"host_key_policy"`
		PrivateKey    string `json:"private_key"`
		Password      string `json:"password"`
		AuthMethod    string `json:"ssh_auth_method"`
		SSHKeyID      string `json:"ssh_key_id"`
	}
	if !decode(w, r, &input) {
		return
	}
	input.City = strings.TrimSpace(input.City)
	if !validLocation(input.CountryCode, input.City) {
		apiError(w, 400, "Проверьте страну и город")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Hostname = strings.TrimSpace(input.Hostname)
	if !validName(input.Name) || input.SSHPort < 1 || input.SSHPort > 65535 || !userRE.MatchString(input.SSHUser) || (net.ParseIP(input.Hostname) == nil && !hostnameRE.MatchString(input.Hostname)) {
		apiError(w, 400, "Проверьте название, адрес и параметры SSH")
		return
	}
	if input.HostKeyPolicy == "" {
		input.HostKeyPolicy = "manual"
	}
	var err error
	switch input.HostKeyPolicy {
	case "manual":
		fingerprint, e := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(input.Fingerprint, "SHA256:"))
		if e != nil || len(fingerprint) != 32 || !strings.HasPrefix(input.Fingerprint, "SHA256:") {
			apiError(w, 400, "Нужен отпечаток ключа хоста в формате SHA256:…")
			return
		}
	case "tofu":
		if input.Fingerprint != "" {
			apiError(w, 400, "Выберите автоматическое получение или ручной отпечаток")
			return
		}
	default:
		apiError(w, 400, "Неизвестный способ проверки ключа хоста")
		return
	}
	if input.AuthMethod == "" {
		input.AuthMethod = "key"
	}
	var secret []byte
	switch input.AuthMethod {
	case "key":
		if input.Password != "" || input.SSHKeyID != "" {
			apiError(w, 400, "Выберите один способ SSH-доступа")
			return
		}
		if _, err = ssh.ParsePrivateKey([]byte(input.PrivateKey)); err != nil {
			apiError(w, 400, "Нужен корректный приватный SSH-ключ без passphrase")
			return
		}
		secret = []byte(input.PrivateKey)
	case "password":
		if input.PrivateKey != "" || input.SSHKeyID != "" || len(input.Password) == 0 || len(input.Password) > 4096 {
			apiError(w, 400, "Укажите только пароль SSH (до 4096 байт)")
			return
		}
		secret = []byte(input.Password)
	case "saved_key":
		if input.PrivateKey != "" || input.Password != "" || !uuidRE.MatchString(input.SSHKeyID) {
			apiError(w, 400, "Выберите сохранённый SSH-ключ")
			return
		}
	default:
		apiError(w, 400, "Неизвестный способ SSH-доступа")
		return
	}
	defer clear(secret)
	if input.HostKeyPolicy == "tofu" {
		input.Fingerprint, err = deployer.DiscoverHostFingerprint(r.Context(), input.Hostname, input.SSHPort)
		if err != nil {
			apiError(w, 502, "Не удалось получить SSH-ключ хоста. Проверьте адрес, порт и доступность VPS с NAS")
			return
		}
	}
	v := store.Server{CountryCode: input.CountryCode, City: input.City, ID: cryptobox.ID(), Name: input.Name, Hostname: input.Hostname, SSHPort: input.SSHPort, SSHUser: input.SSHUser, Fingerprint: input.Fingerprint, AuthMethod: input.AuthMethod, SSHKeyID: input.SSHKeyID, HostKeyPolicy: input.HostKeyPolicy}
	var encrypted []byte
	if secret != nil {
		encrypted = a.box.Seal(secret, "server:"+v.ID)
	}
	v, err = a.db.CreateServer(r.Context(), s.User, v, encrypted)
	input.PrivateKey = ""
	input.Password = ""
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23503" {
			apiError(w, 400, "Сохранённый SSH-ключ больше не существует")
			return
		}
		a.failure(w, err)
		return
	}
	jsonResponse(w, 201, v)
}
func validName(s string) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) > 0 && utf8.RuneCountInString(s) <= 80 && !strings.ContainsAny(s, "\x00\r\n")
}
func (a *App) failure(w http.ResponseWriter, err error) {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		apiError(w, 409, "Такая запись уже существует")
		return
	}
	slog.Error("database operation failed")
	apiError(w, 500, "Не удалось сохранить или получить данные. Попробуйте ещё раз")
}
func jsonResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func apiError(w http.ResponseWriter, status int, message string) {
	jsonResponse(w, status, map[string]string{"error": message})
}
