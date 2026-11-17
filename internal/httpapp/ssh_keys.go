package httpapp

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"github.com/NetherQuartz/cu-chi/internal/cryptobox"
	"github.com/NetherQuartz/cu-chi/internal/store"
	"golang.org/x/crypto/ssh"
	"net/http"
	"strings"
)

func (a *App) sshKeyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/ssh-keys", a.auth(true, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		keys, err := a.db.SSHKeys(r.Context())
		if err != nil {
			a.failure(w, err)
			return
		}
		jsonResponse(w, 200, keys)
	}))
	mux.HandleFunc("POST /api/admin/ssh-keys", a.auth(true, a.createSSHKey))
	mux.HandleFunc("DELETE /api/admin/ssh-keys/{id}", a.auth(true, func(w http.ResponseWriter, r *http.Request, s store.Session) {
		id := r.PathValue("id")
		if !uuidRE.MatchString(id) {
			apiError(w, 404, "Ключ не найден")
			return
		}
		err := a.db.DeleteSSHKey(r.Context(), s.User, id)
		if errors.Is(err, store.ErrConflict) {
			apiError(w, 409, "Ключ используется серверами и не может быть удалён")
			return
		}
		if errors.Is(err, store.ErrNotFound) {
			apiError(w, 404, "Ключ не найден")
			return
		}
		if err != nil {
			a.failure(w, err)
			return
		}
		w.WriteHeader(204)
	}))
}
func (a *App) createSSHKey(w http.ResponseWriter, r *http.Request, s store.Session) {
	var input struct {
		Name       string `json:"name"`
		Mode       string `json:"mode"`
		PrivateKey string `json:"private_key"`
	}
	if !decode(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if !validName(input.Name) {
		apiError(w, 400, "Введите название ключа (до 80 символов)")
		return
	}
	var private []byte
	switch input.Mode {
	case "generate":
		if input.PrivateKey != "" {
			apiError(w, 400, "Выберите создание или импорт ключа")
			return
		}
		_, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			a.failure(w, err)
			return
		}
		defer clear(key)
		block, err := ssh.MarshalPrivateKey(key, "cu-chi")
		if err != nil {
			a.failure(w, err)
			return
		}
		private = pem.EncodeToMemory(block)
		clear(block.Bytes)
	case "import":
		private = []byte(input.PrivateKey)
	default:
		apiError(w, 400, "Выберите создание или импорт ключа")
		return
	}
	defer clear(private)
	signer, err := ssh.ParsePrivateKey(private)
	if err != nil {
		apiError(w, 400, "Нужен корректный приватный SSH-ключ без passphrase")
		return
	}
	k := store.SSHKey{ID: cryptobox.ID(), Name: input.Name, PublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) + " cu-chi", Fingerprint: ssh.FingerprintSHA256(signer.PublicKey())}
	k, err = a.db.CreateSSHKey(r.Context(), s.User, k, a.box.Seal(private, "ssh-key:"+k.ID))
	if err != nil {
		a.failure(w, err)
		return
	}
	jsonResponse(w, 201, k)
}
