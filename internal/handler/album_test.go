package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	idb "copa/internal/db"
	"copa/internal/service"
	"copa/internal/sse"
)

// newTestDeps monta store+hub+notifier+templates sobre um banco temporario.
func newTestDeps(t *testing.T) (*service.Store, *sse.Hub, *Notifier, *Templates) {
	t.Helper()
	database, err := idb.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	store := service.NewStore(database)
	hub := sse.NewHub()
	notifier := NewNotifier(store, hub)
	tmpl, err := NewTemplates(os.DirFS("../../cmd/copa/web"))
	if err != nil {
		t.Fatalf("templates: %v", err)
	}
	return store, hub, notifier, tmpl
}

// loginCookie cria um device para o participante e devolve o cookie de sessao.
func loginCookie(t *testing.T, store *service.Store, participantID int64) *http.Cookie {
	t.Helper()
	dev, err := store.CreateDevice(participantID)
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	return &http.Cookie{Name: cookieName, Value: dev.CookieToken}
}

func TestAlbumWithoutCookieShowsHome(t *testing.T) {
	store, hub, _, tmpl := newTestDeps(t)
	h := NewAlbumHandler(store, hub, tmpl)

	req := httptest.NewRequest(http.MethodGet, "/album", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("esperava 200, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "sticker-grid") {
		t.Fatal("sem sessao nao deveria mostrar o grid do album")
	}
}

func TestAlbumWithDeviceShowsGrid(t *testing.T) {
	store, hub, _, tmpl := newTestDeps(t)
	ana, _ := store.CreateParticipant("Ana", "", "")
	bruno, _ := store.CreateParticipant("Bruno", "", "")
	// Ana ja tem a propria + a do Bruno.
	store.AddToCollection(ana.ID, ana.ID)   //nolint:errcheck
	store.AddToCollection(ana.ID, bruno.ID) //nolint:errcheck

	h := NewAlbumHandler(store, hub, tmpl)
	req := httptest.NewRequest(http.MethodGet, "/album", nil)
	req.AddCookie(loginCookie(t, store, ana.ID))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("esperava 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "sticker-grid") {
		t.Fatalf("device logado deveria ver o grid; body inicio: %.200q", body)
	}
	// Progresso 2/2 = 100%.
	if !strings.Contains(body, "100") {
		t.Errorf("esperava progresso 100%% no album completo")
	}
}

func TestAlbumInvalidCookieClearsAndShowsHome(t *testing.T) {
	store, hub, _, tmpl := newTestDeps(t)
	h := NewAlbumHandler(store, hub, tmpl)

	req := httptest.NewRequest(http.MethodGet, "/album", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: "naoexiste"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("esperava 200, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "sticker-grid") {
		t.Fatal("cookie invalido nao deveria mostrar o grid")
	}
	// Deve limpar o cookie (MaxAge<0).
	cleared := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("cookie invalido deveria ser limpo")
	}
}

func TestRevealShowsNameAndInitial(t *testing.T) {
	_, _, _, tmpl := newTestDeps(t)
	h := NewRevealHandler(tmpl)

	req := httptest.NewRequest(http.MethodGet, "/reveal?name=Jos%C3%A9&complete=0", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("esperava 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "José") {
		t.Errorf("reveal deveria conter o nome 'José'; body: %.300q", body)
	}
}

func TestRevealEmptyNameDoesNotPanic(t *testing.T) {
	_, _, _, tmpl := newTestDeps(t)
	h := NewRevealHandler(tmpl)

	req := httptest.NewRequest(http.MethodGet, "/reveal", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req) // nome vazio: nao deve dar panic ao extrair a inicial

	if rec.Code != http.StatusOK {
		t.Fatalf("esperava 200, got %d", rec.Code)
	}
}

func TestSSEHandlerSendsInitialRanking(t *testing.T) {
	store, hub, notifier, _ := newTestDeps(t)
	// Semeia ranking nao-vazio: o ranking so inclui participantes com device
	// reivindicado (claimed_device_id IS NOT NULL), entao criamos o device da Ana.
	ana, _ := store.CreateParticipant("Ana", "", "")
	if _, err := store.CreateDevice(ana.ID); err != nil {
		t.Fatalf("create device: %v", err)
	}
	store.AddToCollection(ana.ID, ana.ID) //nolint:errcheck

	h := NewSSEHandler(hub, notifier)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/sse", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		h.ServeHTTP(rec, req)
		close(done)
	}()

	time.Sleep(80 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SSEHandler nao encerrou apos cancelar contexto")
	}

	body := rec.Body.String()
	if !strings.Contains(body, "event: ping") {
		t.Errorf("faltou ping inicial; body=%q", body)
	}
	if !strings.Contains(body, "event: ranking") {
		t.Errorf("faltou snapshot de ranking inicial; body=%q", body)
	}
	if !strings.Contains(body, "Ana") {
		t.Errorf("snapshot deveria conter o ranking com 'Ana'; body=%q", body)
	}
}

func TestReclaimCreatesDeviceAndRedirects(t *testing.T) {
	store, hub, notifier, _ := newTestDeps(t)
	ana, _ := store.CreateParticipant("Ana", "", "")

	h := NewReclaimHandler(store, hub, notifier)
	req := httptest.NewRequest(http.MethodPost, "/s/"+ana.Token+"/reclaim", nil)
	req.SetPathValue("token", ana.Token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("reclaim esperava 303, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/album" {
		t.Errorf("reclaim deveria redirecionar pro /album, got %q", loc)
	}
	// Deve setar o cookie de sessao.
	var hasCookie bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName && c.Value != "" {
			hasCookie = true
		}
	}
	if !hasCookie {
		t.Error("reclaim deveria setar o cookie de sessao")
	}
}

func TestReclaimRejectsGet(t *testing.T) {
	store, hub, notifier, _ := newTestDeps(t)
	h := NewReclaimHandler(store, hub, notifier)
	req := httptest.NewRequest(http.MethodGet, "/s/x/reclaim", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET no reclaim esperava 405, got %d", rec.Code)
	}
}

func TestReclaimUnknownToken(t *testing.T) {
	store, hub, notifier, _ := newTestDeps(t)
	h := NewReclaimHandler(store, hub, notifier)
	req := httptest.NewRequest(http.MethodPost, "/s/inexistente/reclaim", nil)
	req.SetPathValue("token", "inexistente")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("token inexistente esperava 404, got %d", rec.Code)
	}
}
