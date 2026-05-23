package sse

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Hub manages SSE client connections.
type Hub struct {
	mu      sync.RWMutex
	clients map[chan string]struct{}
}

// NewHub creates a new SSE hub.
func NewHub() *Hub {
	return &Hub{clients: make(map[chan string]struct{})}
}

// Subscribe registers a client channel.
func (h *Hub) Subscribe() chan string {
	ch := make(chan string, 8)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

// Unsubscribe removes a client channel.
func (h *Hub) Unsubscribe(ch chan string) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
	close(ch)
}

// FormatMessage formats an SSE event frame.
func FormatMessage(event, data string) string {
	return fmt.Sprintf("event: %s\ndata: %s\n\n", event, data)
}

// Broadcast sends a message to all connected clients.
func (h *Hub) Broadcast(event, data string) {
	msg := FormatMessage(event, data)
	h.mu.RLock()
	for ch := range h.clients {
		select {
		case ch <- msg:
		default:
			// drop if client is slow
		}
	}
	h.mu.RUnlock()
}

// ServeHTTP handles an SSE connection for a given client channel.
// initial holds messages to send immediately on connect (e.g. a state snapshot),
// so a client that reconnects after a network blip gets fresh data right away.
func ServeSSE(w http.ResponseWriter, r *http.Request, ch chan string, initial ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	// Send initial ping + any state snapshot.
	fmt.Fprintf(w, "event: ping\ndata: ok\n\n")
	for _, msg := range initial {
		fmt.Fprint(w, msg)
	}
	flusher.Flush()

	// Heartbeat keeps the connection alive through phones/proxies that drop idle streams.
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case msg, open := <-ch:
			if !open {
				return
			}
			fmt.Fprint(w, msg)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprint(w, ": ping\n\n") // comment frame, ignored by EventSource
			flusher.Flush()
		case <-ctx.Done():
			return
		}
	}
}
