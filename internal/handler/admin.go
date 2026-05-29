package handler

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"copa/internal/config"
	"copa/internal/gemini"
	"copa/internal/model"
	"copa/internal/qr"
	"copa/internal/service"
	"copa/internal/sse"

	"golang.org/x/crypto/bcrypt"
)

const adminCookieName = config.AdminCookieName

// AdminHandler handles /admin routes.
type AdminHandler struct {
	store      *service.Store
	hub        *sse.Hub
	tmpl       *Templates
	uploadsDir string
	dataDir    string
	listenAddr string // ex: ":8080" — usado para sugerir URLs no diagnóstico
	notifier   *Notifier

	mu            sync.Mutex
	sessions      map[string]time.Time // token de sessao -> expiracao
	stopCleanup   chan struct{}
	cleanupTicker *time.Ticker
}

func NewAdminHandler(store *service.Store, hub *sse.Hub, tmpl *Templates, uploadsDir, dataDir, listenAddr string, notifier *Notifier) *AdminHandler {
	h := &AdminHandler{
		store: store, hub: hub, tmpl: tmpl,
		uploadsDir: uploadsDir, dataDir: dataDir, listenAddr: listenAddr, notifier: notifier,
		sessions:    map[string]time.Time{},
		stopCleanup: make(chan struct{}),
	}

	// Start session cleanup goroutine
	go h.cleanupExpiredSessions()

	return h
}

// Close gracefully shuts down the admin handler and cleanup goroutine.
func (h *AdminHandler) Close() {
	h.mu.Lock()
	if h.cleanupTicker != nil {
		h.cleanupTicker.Stop()
	}
	h.mu.Unlock()
	close(h.stopCleanup)
}

// cleanupExpiredSessions periodically removes expired sessions from memory.
func (h *AdminHandler) cleanupExpiredSessions() {
	ticker := time.NewTicker(config.SessionCleanup)
	h.mu.Lock()
	h.cleanupTicker = ticker
	h.mu.Unlock()
	defer ticker.Stop()

	for {
		select {
		case <-h.stopCleanup:
			return
		case <-ticker.C:
			h.mu.Lock()
			now := time.Now()
			for token, exp := range h.sessions {
				if now.After(exp) {
					delete(h.sessions, token)
				}
			}
			h.mu.Unlock()
		}
	}
}

func newSessionToken() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		log.Printf("warning: failed to generate session token: %v", err)
		// Fall back to a less-secure but valid token on error
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func (h *AdminHandler) requireAuth(r *http.Request) bool {
	c, err := r.Cookie(adminCookieName)
	if err != nil || c.Value == "" {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	exp, ok := h.sessions[c.Value]
	if !ok || time.Now().After(exp) {
		delete(h.sessions, c.Value)
		return false
	}
	return true
}

// ServeHTTP is the router for all /admin/* paths.
func (h *AdminHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// Login page (no auth needed)
	if path == "/admin" || path == "/admin/" || path == "/admin/login" {
		if r.Method == http.MethodPost {
			h.handleLogin(w, r)
			return
		}
		h.tmpl.Render(w, "admin_login.html", nil)
		return
	}

	// All other admin routes require auth
	if !h.requireAuth(r) {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}

	switch {
	case path == "/admin/dashboard":
		h.handleDashboard(w, r)
	case path == "/admin/preflight":
		h.handlePreflight(w, r)
	case path == "/admin/participants":
		h.handleParticipants(w, r)
	case path == "/admin/participants/new":
