package handler

import (
	"net/http"
	"net/url"

	"copa/internal/model"
	"copa/internal/service"
	"copa/internal/sse"
)

// StickerHandler handles the QR scan entry point GET /s/{token}.
type StickerHandler struct {
	store    *service.Store
	hub      *sse.Hub
	tmpl     *Templates
	notifier *Notifier
}

func NewStickerHandler(store *service.Store, hub *sse.Hub, tmpl *Templates, notifier *Notifier) *StickerHandler {
	return &StickerHandler{store: store, hub: hub, tmpl: tmpl, notifier: notifier}
}

// ServeHTTP handles GET /s/{token}
func (h *StickerHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		http.Error(w, "token missing", http.StatusBadRequest)
		return
	}

	// Find sticker participant
	sticker, err := h.store.GetParticipantByToken(token)
	if err != nil {
		renderError(w, h.tmpl, "QR inválido. Chame o organizador.", http.StatusNotFound)
		return
	}
	if !sticker.Active {
		renderError(w, h.tmpl, "Esta figurinha não está ativa. Chame o organizador.", http.StatusGone)
		return
	}

	// Check identity from cookie
	cookieToken := getCookieToken(r)
	if cookieToken == "" {
		// No identity: show confirmation screen
		h.showConfirm(w, r, sticker)
		return
	}

	device, err := h.store.GetDeviceByCookie(cookieToken)
	if err != nil {
		// Cookie exists but device not in DB (e.g., DB was reset) - treat as no identity
		clearCookie(w)
		h.showConfirm(w, r, sticker)
		return
	}

	owner, err := h.store.GetParticipantByID(device.ParticipantID)
	if err != nil {
		clearCookie(w)
		h.showConfirm(w, r, sticker)
		return
	}

	// We have identity: owner is scanning
	if sticker.ID == owner.ID {
		// Scanning own sticker: recovery/home redirect
		// Re-link device to this participant (covers cookie from different device)
		if err := h.store.ReassignDevice(device.ID, owner.ID); err == nil {
			setCookie(w, device.CookieToken)
		}
		http.Redirect(w, r, "/album", http.StatusSeeOther)
		return
	}

	// Scanning someone else's sticker: add to collection
	isNew, err := h.store.AddToCollection(owner.ID, sticker.ID)
	if err != nil {
		http.Error(w, "Erro ao coletar figurinha: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Broadcast ranking update via SSE
	h.broadcastRanking()

	// Check completion
	complete, _ := h.store.IsComplete(owner.ID)
	if complete && isNew {
		completedAt, _ := h.store.CompletedAt(owner.ID)
		h.broadcastComplete(owner, completedAt)
	}

	// Redirect to reveal page if new, otherwise to album
	if isNew {
		http.Redirect(w, r, "/reveal?name="+urlEncode(sticker.Name)+"&photo="+urlEncode(sticker.PhotoPath)+"&complete="+boolStr(complete), http.StatusSeeOther)
	} else {
		http.Redirect(w, r, "/album?already=1", http.StatusSeeOther)
	}
}

func (h *StickerHandler) showConfirm(w http.ResponseWriter, r *http.Request, sticker *model.Participant) {
	h.tmpl.Render(w, "confirm.html", map[string]interface{}{
		"Sticker": sticker,
	})
}

// ConfirmHandler handles POST /s/{token}/confirm (user says "Yes, I'm this person")
type ConfirmHandler struct {
	store    *service.Store
	hub      *sse.Hub
	tmpl     *Templates
	notifier *Notifier
}

func NewConfirmHandler(store *service.Store, hub *sse.Hub, tmpl *Templates, notifier *Notifier) *ConfirmHandler {
	return &ConfirmHandler{store: store, hub: hub, tmpl: tmpl, notifier: notifier}
}

func (h *ConfirmHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := r.PathValue("token")

	sticker, err := h.store.GetParticipantByToken(token)
	if err != nil {
		renderError(w, h.tmpl, "QR inválido. Chame o organizador.", http.StatusNotFound)
		return
	}
	if !sticker.Active {
		renderError(w, h.tmpl, "Esta figurinha não está ativa.", http.StatusGone)
		return
	}

	r.ParseForm()
	choice := r.FormValue("choice")

	if choice == "no" {
		h.tmpl.Render(w, "not_me.html", map[string]interface{}{
			"Sticker": sticker,
		})
		return
	}

	// choice == "yes": create device and link to sticker
	cookieToken := getCookieToken(r)

	if cookieToken != "" {
		// Device exists, re-assign (re-registration recovery)
		device, err := h.store.GetDeviceByCookie(cookieToken)
		if err == nil {
			if err := h.store.ReassignDevice(device.ID, sticker.ID); err != nil {
				http.Error(w, "Erro ao registrar: "+err.Error(), http.StatusInternalServerError)
				return
			}
			// Add own sticker
			h.store.AddToCollection(sticker.ID, sticker.ID) //nolint:errcheck
			h.broadcastRanking()
			setCookie(w, device.CookieToken)
			http.Redirect(w, r, "/album", http.StatusSeeOther)
			return
		}
	}

	// Check if sticker already claimed by another device
	if sticker.ClaimedDeviceID != nil {
		// Sticker already claimed — show re-claim confirmation
		h.tmpl.Render(w, "reclaim.html", map[string]interface{}{
			"Sticker": sticker,
			"Token":   token,
		})
		return
	}

	// Create new device
	device, err := h.store.CreateDevice(sticker.ID)
	if err != nil {
		http.Error(w, "Erro ao criar sessão: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Add own sticker automatically
	h.store.AddToCollection(sticker.ID, sticker.ID) //nolint:errcheck
	h.broadcastRanking()

	setCookie(w, device.CookieToken)
	http.Redirect(w, r, "/album", http.StatusSeeOther)
}

// ReclaimHandler handles POST /s/{token}/reclaim
type ReclaimHandler struct {
	store    *service.Store
	hub      *sse.Hub
	notifier *Notifier
}

func NewReclaimHandler(store *service.Store, hub *sse.Hub, notifier *Notifier) *ReclaimHandler {
	return &ReclaimHandler{store: store, hub: hub, notifier: notifier}
}

func (h *ReclaimHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := r.PathValue("token")
	sticker, err := h.store.GetParticipantByToken(token)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// Create a new device for the reclaim
	device, err := h.store.CreateDevice(sticker.ID)
	if err != nil {
		http.Error(w, "Erro ao reclamar: "+err.Error(), http.StatusInternalServerError)
		return
	}
	setCookie(w, device.CookieToken)
	h.broadcastRanking()
	http.Redirect(w, r, "/album", http.StatusSeeOther)
}

func (h *StickerHandler) broadcastRanking() { h.notifier.Ranking() }

func (h *StickerHandler) broadcastComplete(owner *model.Participant, completedAt interface{}) {
	h.notifier.Complete(owner, completedAt)
}

func (h *ConfirmHandler) broadcastRanking() { h.notifier.Ranking() }

func (h *ReclaimHandler) broadcastRanking() { h.notifier.Ranking() }

func urlEncode(s string) string {
	return url.QueryEscape(s)
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
