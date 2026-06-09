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
	"sort"
	"strings"
	"syscall"
	"time"

	"copa/internal/app"
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
			backupFull(store, dataDir, uploadsDir)
		}
	}()

	// Endereco de bind (COPA_ADDR permite trocar a porta, ex: ":8090")
	addr := ":8080"
	if v := strings.TrimSpace(os.Getenv("COPA_ADDR")); v != "" {
		addr = v
	}

	// Router (montado em internal/app para paridade com os testes)
	staticFS, _ := fs.Sub(webSubFS, "static")
	mux := app.NewRouter(app.Deps{
		Store:      store,
		Hub:        hub,
		Notifier:   notifier,
		Tmpl:       tmpl,
		StaticFS:   staticFS,
		UploadsDir: uploadsDir,
		DataDir:    dataDir,
		ListenAddr: addr,
	})

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
	backupFull(store, dataDir, uploadsDir) // backup final
	log.Print("encerrado com seguranca.")
}

// backupFull grava um backup COMPLETO (banco + fotos) como copa-backup-*.zip.
// Inclui uploads/ para que as figurinhas geradas pela IA — insubstituíveis —
// estejam na rede de segurança automática, não só o banco.
func backupFull(store *service.Store, dataDir, uploadsDir string) {
	dst := filepath.Join(dataDir, fmt.Sprintf("copa-backup-%s.zip", time.Now().Format("20060102-150405")))
	if err := handler.WriteFullBackup(store, uploadsDir, dst); err != nil {
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
	// Apenas os backups de verdade (copa-backup-*.db). Arquivos temporarios de
	// download/restore (copa-download-*, copa-full-db-*, copa-restore-*) NAO contam:
	// se o processo morrer no meio de uma dessas operacoes eles ficam, e conta-los
	// aqui poderia empurrar e apagar backups reais.
	type backup struct {
		path    string
		modTime time.Time
	}
	var backups []backup
	for _, e := range entries {
		if e.IsDir() || !matchesBackupName(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		backups = append(backups, backup{filepath.Join(dataDir, e.Name()), info.ModTime()})
	}
	// Mais antigos primeiro; remove ate sobrarem 5.
	sort.Slice(backups, func(i, j int) bool { return backups[i].modTime.Before(backups[j].modTime) })
	for len(backups) > 5 {
		os.Remove(backups[0].path) //nolint:errcheck
		backups = backups[1:]
	}
}

// matchesBackupName identifica os arquivos de backup periodico/final
// (copa-backup-AAAAMMDD-HHMMSS.zip, ou .db de versoes antigas), excluindo os
// temporarios de download/restore (copa-download-*, copa-restore-*).
func matchesBackupName(name string) bool {
	return strings.HasPrefix(name, "copa-backup-") &&
		(strings.HasSuffix(name, ".zip") || strings.HasSuffix(name, ".db"))
}
