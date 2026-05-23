package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"copa/internal/db"
	"copa/internal/handler"
	"copa/internal/service"
	"copa/internal/sse"

	"golang.org/x/crypto/bcrypt"
)

//go:embed web
var webFS embed.FS

func main() {
	// Determine working directory (where the binary is, or current dir)
	exeDir, err := os.Executable()
	if err != nil {
		log.Fatal("cannot determine executable path:", err)
	}
	baseDir := filepath.Dir(exeDir)
	// During `go run`, exeDir is in a temp dir; use cwd instead
	if _, err := os.Stat(filepath.Join(baseDir, "data")); err != nil {
		baseDir, _ = os.Getwd()
	}

	dataDir := filepath.Join(baseDir, "data")
	uploadsDir := filepath.Join(baseDir, "uploads")

	if err := os.MkdirAll(uploadsDir, 0755); err != nil {
		log.Fatal("create uploads dir:", err)
	}

	// Open database
	database, err := db.Open(dataDir)
	if err != nil {
		log.Fatal("open db:", err)
	}
	defer database.Close()

	// Store
	store := service.NewStore(database)

	// Senha de admin via variavel de ambiente (fecha o buraco do "primeiro acesso define a senha").
	if pw := os.Getenv("COPA_ADMIN_PASSWORD"); pw != "" {
		if hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost); err == nil {
			if err := store.SetAdminPasswordHash(string(hash)); err != nil {
				log.Printf("aviso: nao foi possivel definir a senha de admin via env: %v", err)
			} else {
				log.Print("senha de admin definida a partir de COPA_ADMIN_PASSWORD")
			}
		}
	}

	// SSE hub + notifier (broadcast de ranking com throttle)
	hub := sse.NewHub()
	notifier := handler.NewNotifier(store, hub)

	// Templates from embedded FS
	webSubFS, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatal("sub fs:", err)
	}
	tmpl, err := handler.NewTemplates(webSubFS)
	if err != nil {
		log.Fatal("parse templates:", err)
	}

	// Periodic backup (every 30 min)
	go func() {
		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			backupDB(store, dataDir)
		}
	}()

	// Router
	mux := http.NewServeMux()

	// Static files
	staticFS, _ := fs.Sub(webSubFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// Uploads (photos)
	mux.Handle("GET /uploads/", http.StripPrefix("/uploads/", http.FileServer(http.Dir(uploadsDir))))

	// Health check
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})

	// Home redirect (exact "/" only, so it doesn't act as a catch-all)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/album", http.StatusSeeOther)
	})

	// Sticker / QR flow
	stickerH := handler.NewStickerHandler(store, hub, tmpl, notifier)
	confirmH := handler.NewConfirmHandler(store, hub, tmpl, notifier)
	reclaimH := handler.NewReclaimHandler(store, hub, notifier)

	mux.Handle("GET /s/{token}", stickerH)
	mux.Handle("POST /s/{token}/confirm", confirmH)
	mux.Handle("POST /s/{token}/reclaim", reclaimH)

	// Album & reveal
	albumH := handler.NewAlbumHandler(store, hub, tmpl)
	revealH := handler.NewRevealHandler(tmpl)
	mux.Handle("GET /album", albumH)
	mux.Handle("GET /reveal", revealH)
	mux.Handle("POST /logout", handler.NewLogoutHandler())

	// SSE
	sseH := handler.NewSSEHandler(hub, notifier)
	mux.Handle("GET /sse", sseH)

	// TV screen
	tvH := handler.NewTVHandler(store, hub, tmpl)
	mux.Handle("GET /tv", tvH)

	// Admin
	addr := ":8080"
	adminH := handler.NewAdminHandler(store, hub, tmpl, uploadsDir, dataDir, addr, notifier)
	mux.Handle("/admin", adminH)
	mux.Handle("/admin/", adminH)
	log.Printf("Copa server starting on http://0.0.0.0%s", addr)
	log.Printf("Admin panel: http://localhost%s/admin", addr)
	log.Printf("TV screen:   http://localhost%s/tv", addr)

	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// SSE usa conexoes de longa duracao; nao limitar a escrita nessas rotas.
	srv.WriteTimeout = 0

	// Shutdown gracioso: Ctrl+C / fechar terminal -> drena conexoes, faz checkpoint do WAL
	// e um backup final, evitando perda/corrupcao de dados.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("server error:", err)
		}
	}()
	log.Print("pronto — Ctrl+C para encerrar com seguranca.")

	<-ctx.Done()
	log.Print("encerrando...")
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Printf("shutdown warning: %v", err)
	}
	if err := store.Checkpoint(); err != nil {
		log.Printf("checkpoint warning: %v", err)
	}
	backupDB(store, dataDir) // backup final
	log.Print("encerrado com seguranca.")
}

func backupDB(store *service.Store, dataDir string) {
	dst := filepath.Join(dataDir, fmt.Sprintf("copa-backup-%s.db", time.Now().Format("20060102-150405")))
	if err := store.BackupTo(dst); err != nil {
		log.Printf("backup error: %v", err)
		return
	}
	log.Printf("backup created: %s", dst)
	// Keep only last 5 backups
	cleanOldBackups(dataDir)
}

func cleanOldBackups(dataDir string) {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return
	}
	var backups []string
	for _, e := range entries {
		if !e.IsDir() && len(e.Name()) > 12 && e.Name()[:5] == "copa-" && e.Name()[len(e.Name())-3:] == ".db" {
			backups = append(backups, filepath.Join(dataDir, e.Name()))
		}
	}
	for len(backups) > 5 {
		os.Remove(backups[0]) //nolint:errcheck
		backups = backups[1:]
	}
}
