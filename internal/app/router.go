// Package app monta o roteador HTTP da aplicacao. Foi extraido de cmd/copa/main.go
// para que o servidor de testes (incl. E2E de navegador) use exatamente as mesmas
// rotas que a producao — sem duplicar o wiring nem divergir (ex.: /sse, /static).
package app

import (
	"fmt"
	"io/fs"
	"net/http"

	"copa/internal/handler"
	"copa/internal/service"
	"copa/internal/sse"
)

// Deps reune tudo que o roteador precisa para montar os handlers.
type Deps struct {
	Store      *service.Store
	Hub        *sse.Hub
	Notifier   *handler.Notifier
	Tmpl       *handler.Templates
	StaticFS   fs.FS  // arvore de arquivos estaticos (web/static)
	UploadsDir string // diretorio das fotos enviadas
	DataDir    string // diretorio do banco/backups (usado pelo admin)
	ListenAddr string // endereco de bind, exibido no painel admin
}

// NewRouter monta o *http.ServeMux com todas as rotas da aplicacao.
func NewRouter(d Deps) *http.ServeMux {
	mux := http.NewServeMux()

	// Arquivos estaticos
	if d.StaticFS != nil {
		mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(d.StaticFS))))
	}

	// Uploads (fotos)
	if d.UploadsDir != "" {
		mux.Handle("GET /uploads/", http.StripPrefix("/uploads/", http.FileServer(http.Dir(d.UploadsDir))))
	}

	// Health check
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})

	// Home redirect (exact "/" only, so it doesn't act as a catch-all)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/album", http.StatusSeeOther)
	})

	// Sticker / QR flow
	mux.Handle("GET /s/{token}", handler.NewStickerHandler(d.Store, d.Hub, d.Tmpl, d.Notifier))
	mux.Handle("POST /s/{token}/confirm", handler.NewConfirmHandler(d.Store, d.Hub, d.Tmpl, d.Notifier))
	mux.Handle("POST /s/{token}/reclaim", handler.NewReclaimHandler(d.Store, d.Hub, d.Notifier))

	// Album & reveal
	mux.Handle("GET /album", handler.NewAlbumHandler(d.Store, d.Hub, d.Tmpl))
	mux.Handle("GET /reveal", handler.NewRevealHandler(d.Tmpl))
	mux.Handle("POST /logout", handler.NewLogoutHandler())

	// SSE
	mux.Handle("GET /sse", handler.NewSSEHandler(d.Hub, d.Notifier))

	// TV screen
	mux.Handle("GET /tv", handler.NewTVHandler(d.Store, d.Hub, d.Tmpl))

	// Admin
	adminH := handler.NewAdminHandler(d.Store, d.Hub, d.Tmpl, d.UploadsDir, d.DataDir, d.ListenAddr, d.Notifier)
	mux.Handle("/admin", adminH)
	mux.Handle("/admin/", adminH)

	return mux
}
