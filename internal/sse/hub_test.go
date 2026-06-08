package sse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFormatMessage(t *testing.T) {
	got := FormatMessage("ranking", "{\"a\":1}")
	want := "event: ranking\ndata: {\"a\":1}\n\n"
	if got != want {
		t.Fatalf("FormatMessage = %q, want %q", got, want)
	}
}

func TestSubscribeUnsubscribe(t *testing.T) {
	h := NewHub()
	ch := h.Subscribe()

	h.mu.RLock()
	n := len(h.clients)
	h.mu.RUnlock()
	if n != 1 {
		t.Fatalf("apos Subscribe esperava 1 cliente, got %d", n)
	}

	h.Unsubscribe(ch)
	h.mu.RLock()
	n = len(h.clients)
	h.mu.RUnlock()
	if n != 0 {
		t.Fatalf("apos Unsubscribe esperava 0 clientes, got %d", n)
	}

	// O canal deve estar fechado.
	if _, open := <-ch; open {
		t.Fatal("canal deveria estar fechado apos Unsubscribe")
	}
}

func TestBroadcastDeliversToAllSubscribers(t *testing.T) {
	h := NewHub()
	a := h.Subscribe()
	b := h.Subscribe()
	defer h.Unsubscribe(a)
	defer h.Unsubscribe(b)

	h.Broadcast("ping", "hello")

	want := FormatMessage("ping", "hello")
	for i, ch := range []chan string{a, b} {
		select {
		case msg := <-ch:
			if msg != want {
				t.Fatalf("assinante %d recebeu %q, want %q", i, msg, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("assinante %d nao recebeu o broadcast", i)
		}
	}
}

// Cliente lento (canal cheio) nao deve travar o Broadcast: a mensagem e descartada.
func TestBroadcastDropsSlowClient(t *testing.T) {
	h := NewHub()
	ch := h.Subscribe()
	defer h.Unsubscribe(ch)

	// Enche o buffer (cap=8) sem ler.
	for i := 0; i < 8; i++ {
		h.Broadcast("e", "x")
	}
	// Este broadcast extra deve cair no default (drop), sem bloquear.
	done := make(chan struct{})
	go func() {
		h.Broadcast("e", "overflow")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Broadcast travou com cliente lento (canal cheio)")
	}

	// O buffer deve conter as 8 primeiras, nao a 'overflow'.
	for i := 0; i < 8; i++ {
		if msg := <-ch; !strings.Contains(msg, "data: x") {
			t.Fatalf("mensagem %d inesperada: %q", i, msg)
		}
	}
	select {
	case extra := <-ch:
		t.Fatalf("nao deveria haver 9a mensagem, got %q", extra)
	default:
	}
}

func TestServeSSESendsPingAndInitialSnapshot(t *testing.T) {
	ch := make(chan string, 4)
	snapshot := FormatMessage("ranking", "snap")

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/sse", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		ServeSSE(rec, req, ch, snapshot)
		close(done)
	}()

	// Empurra uma mensagem e encerra via cancelamento de contexto.
	ch <- FormatMessage("ranking", "live")
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ServeSSE nao encerrou apos cancelamento do contexto")
	}

	body := rec.Body.String()
	if !strings.Contains(body, "event: ping\ndata: ok") {
		t.Errorf("faltou ping inicial; body=%q", body)
	}
	if !strings.Contains(body, "data: snap") {
		t.Errorf("faltou snapshot inicial; body=%q", body)
	}
	if !strings.Contains(body, "data: live") {
		t.Errorf("faltou mensagem ao vivo; body=%q", body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
}

func TestServeSSEStopsWhenChannelClosed(t *testing.T) {
	ch := make(chan string, 1)
	req := httptest.NewRequest(http.MethodGet, "/sse", nil)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		ServeSSE(rec, req, ch)
		close(done)
	}()

	close(ch) // fechar o canal deve encerrar o ServeSSE
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ServeSSE nao encerrou apos fechamento do canal")
	}
}
