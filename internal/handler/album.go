package handler

import (
	"net/http"

	"copa/internal/service"
	"copa/internal/sse"
)

// AlbumHandler handles GET /album
type AlbumHandler struct {
	store *service.Store
	hub   *sse.Hub
	tmpl  *Templates
}

func NewAlbumHandler(store *service.Store, hub *sse.Hub, tmpl *Templates) *AlbumHandler {
	return &AlbumHandler{store: store, hub: hub, tmpl: tmpl}
}

func (h *AlbumHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cookieToken := getCookieToken(r)
	if cookieToken == "" {
		// Sem sessao: mostra a tela de boas-vindas (em vez de redirecionar pra "/",
		// que redirecionaria de volta pra ca -> loop infinito).
		h.tmpl.Render(w, "home.html", nil)
		return
	}

	device, err := h.store.GetDeviceByCookie(cookieToken)
	if err != nil {
		clearCookie(w)
		h.tmpl.Render(w, "home.html", nil)
		return
	}

	owner, err := h.store.GetParticipantByID(device.ParticipantID)
	if err != nil {
		clearCookie(w)
		h.tmpl.Render(w, "home.html", nil)
		return
	}

	// Get all active participants
	all, err := h.store.ListActiveParticipants()
	if err != nil {
		http.Error(w, "Erro ao carregar participantes: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Get owner's collection
	collection, err := h.store.GetCollection(owner.ID)
	if err != nil {
		http.Error(w, "Erro ao carregar coleção: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Build set of collected IDs
	collected := map[int64]bool{}
	for _, c := range collection {
		collected[c.StickerID] = true
	}

	type slot struct {
		Participant interface{}
		Collected   bool
	}

	var slots []slot
	for _, p := range all {
		slots = append(slots, slot{
			Participant: p,
			Collected:   collected[p.ID],
		})
	}

	complete, _ := h.store.IsComplete(owner.ID)
	// Conta apenas figurinhas de participantes ATIVOS, para o progresso nunca passar de 100%.
	count, _ := h.store.CountActiveCollected(owner.ID)
	total := len(all)

	var progress int
	if total > 0 {
		progress = (count * 100) / total
	}

	already := r.URL.Query().Get("already") == "1"

	h.tmpl.Render(w, "album.html", map[string]interface{}{
		"Owner":    owner,
		"Slots":    slots,
		"Count":    count,
		"Total":    total,
		"Progress": progress,
		"Complete": complete,
		"Already":  already,
	})
}

// LogoutHandler handles POST /logout — limpa a sessao do convidado para que um
// aparelho compartilhado possa trocar de jogador. Os dados ficam salvos por
// participante no banco; basta escanear a propria figurinha de novo para voltar.
type LogoutHandler struct{}

func NewLogoutHandler() *LogoutHandler { return &LogoutHandler{} }

func (h *LogoutHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	clearCookie(w)
	http.Redirect(w, r, "/album", http.StatusSeeOther)
}

// RevealHandler handles GET /reveal
type RevealHandler struct {
	tmpl *Templates
}

func NewRevealHandler(tmpl *Templates) *RevealHandler {
	return &RevealHandler{tmpl: tmpl}
}

func (h *RevealHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// r.URL.Query().Get ja faz o percent-decode; nao reescapar (evita double-unescape).
	name := r.URL.Query().Get("name")
	photo := r.URL.Query().Get("photo")
	complete := r.URL.Query().Get("complete") == "1"

	h.tmpl.Render(w, "reveal.html", map[string]interface{}{
		"Name":     name,
		"Photo":    photo,
		"Complete": complete,
	})
}

// SSEHandler handles GET /sse
type SSEHandler struct {
	hub      *sse.Hub
	notifier *Notifier
}

func NewSSEHandler(hub *sse.Hub, notifier *Notifier) *SSEHandler {
	return &SSEHandler{hub: hub, notifier: notifier}
}

func (h *SSEHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ch := h.hub.Subscribe()
	defer h.hub.Unsubscribe(ch)
	// Envia o ranking atual imediatamente, para que um cliente que reconecta
	// apos uma queda de rede ja receba o estado fresco (telao nao fica congelado).
	var initial []string
	if snap := h.notifier.RankingSnapshot(); snap != "" {
		initial = append(initial, snap)
	}
	sse.ServeSSE(w, r, ch, initial...)
}
