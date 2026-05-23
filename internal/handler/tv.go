package handler

import (
	"net/http"
	"time"

	"copa/internal/model"
	"copa/internal/service"
	"copa/internal/sse"
)

// TVHandler handles GET /tv
type TVHandler struct {
	store *service.Store
	hub   *sse.Hub
	tmpl  *Templates
}

func NewTVHandler(store *service.Store, hub *sse.Hub, tmpl *Templates) *TVHandler {
	return &TVHandler{store: store, hub: hub, tmpl: tmpl}
}

func (h *TVHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setting, _ := h.store.GetSetting()
	total, _ := h.store.CountActiveParticipants()

	var kickoffStr string
	if setting.KickoffAt != nil {
		kickoffStr = setting.KickoffAt.UTC().Format("2006-01-02T15:04:05Z")
	}

	// Apos o apito, a classificacao congela no kickoff e anunciamos o campeao.
	gameStarted := setting.KickoffAt != nil && !time.Now().Before(*setting.KickoffAt)
	var ranking []*model.RankEntry
	var champion *model.RankEntry
	if gameStarted {
		ranking, _ = h.store.GetFinalRanking(*setting.KickoffAt)
		champion, _ = h.store.Winner(setting.KickoffAt)
	} else {
		ranking, _ = h.store.GetRanking()
	}

	// Build QR for /tv access help
	qrURL := setting.BaseURL
	qrB64, _ := generateQRBase64(qrURL)

	h.tmpl.Render(w, "tv.html", map[string]interface{}{
		"Ranking":     ranking,
		"Total":       total,
		"KickoffAt":   kickoffStr,
		"BaseURL":     setting.BaseURL,
		"QRBase64":    qrB64,
		"GameStarted": gameStarted,
		"Champion":    champion,
	})
}
