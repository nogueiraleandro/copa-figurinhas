package handler

import (
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"copa/internal/model"
	"copa/internal/qr"
	"copa/internal/service"
	"copa/internal/sse"

	"golang.org/x/crypto/bcrypt"
)

const adminCookieName = "copa_admin"

// AdminHandler handles /admin routes.
type AdminHandler struct {
	store      *service.Store
	hub        *sse.Hub
	tmpl       *Templates
	uploadsDir string
	dataDir    string
	listenAddr string // ex: ":8080" — usado para sugerir URLs no diagnóstico
	notifier   *Notifier

	mu       sync.Mutex
	sessions map[string]time.Time // token de sessao -> expiracao
}

func NewAdminHandler(store *service.Store, hub *sse.Hub, tmpl *Templates, uploadsDir, dataDir, listenAddr string, notifier *Notifier) *AdminHandler {
	return &AdminHandler{
		store: store, hub: hub, tmpl: tmpl,
		uploadsDir: uploadsDir, dataDir: dataDir, listenAddr: listenAddr, notifier: notifier,
		sessions: map[string]time.Time{},
	}
}

func newSessionToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
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
	case path == "/admin/participants":
		h.handleParticipants(w, r)
	case path == "/admin/participants/new":
		h.handleNewParticipant(w, r)
	case strings.HasPrefix(path, "/admin/participants/") && strings.HasSuffix(path, "/edit"):
		h.handleEditParticipant(w, r)
	case strings.HasPrefix(path, "/admin/participants/") && strings.HasSuffix(path, "/delete"):
		h.handleDeleteParticipant(w, r)
	case path == "/admin/bulk":
		h.handleBulkImport(w, r)
	case path == "/admin/qrsheet":
		h.handleQRSheet(w, r)
	case strings.HasPrefix(path, "/admin/qr/"):
		h.handleQRImage(w, r)
	case path == "/admin/settings":
		h.handleSettings(w, r)
	case path == "/admin/lock":
		h.handleLockRoster(w, r)
	case path == "/admin/backup":
		h.handleBackup(w, r)
	case path == "/admin/export":
		h.handleExport(w, r)
	case path == "/admin/transfer":
		h.handleTransfer(w, r)
	case path == "/admin/cards":
		h.handleCards(w, r)
	case path == "/admin/sistema":
		h.handleSystem(w, r)
	case path == "/admin/sistema/reset":
		h.handleReset(w, r)
	case path == "/admin/sistema/restore":
		h.handleRestore(w, r)
	case path == "/admin/logout":
		h.handleLogout(w, r)
	default:
		http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
	}
}

func (h *AdminHandler) handleLogin(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	password := r.FormValue("password")

	setting, err := h.store.GetSetting()
	if err != nil {
		http.Error(w, "DB error", http.StatusInternalServerError)
		return
	}

	if setting.AdminPasswordHash == "" {
		// First login: set password
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			http.Error(w, "hash error", http.StatusInternalServerError)
			return
		}
		setting.AdminPasswordHash = string(hash)
		h.store.SaveSetting(setting) //nolint:errcheck
	} else {
		if err := bcrypt.CompareHashAndPassword([]byte(setting.AdminPasswordHash), []byte(password)); err != nil {
			h.tmpl.Render(w, "admin_login.html", map[string]interface{}{"Error": "Senha incorreta"})
			return
		}
	}

	// Cria uma sessao com token aleatorio guardado no servidor (cookie nao e mais forjavel).
	token := newSessionToken()
	h.mu.Lock()
	h.sessions[token] = time.Now().Add(24 * time.Hour)
	h.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     adminCookieName,
		Value:    token,
		Path:     "/admin",
		Expires:  time.Now().Add(24 * time.Hour),
		MaxAge:   int((24 * time.Hour).Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
}

func (h *AdminHandler) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(adminCookieName); err == nil {
		h.mu.Lock()
		delete(h.sessions, c.Value)
		h.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name:    adminCookieName,
		Value:   "",
		Path:    "/admin",
		Expires: time.Unix(0, 0),
		MaxAge:  -1,
	})
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (h *AdminHandler) handleDashboard(w http.ResponseWriter, r *http.Request) {
	participants, _ := h.store.ListParticipants()
	ranking, _ := h.store.GetRanking()
	setting, _ := h.store.GetSetting()
	total, _ := h.store.CountActiveParticipants()

	// Quem ainda nao entrou: ativos sem device reivindicado.
	var unregistered []*model.Participant
	registered := 0
	for _, p := range participants {
		if !p.Active {
			continue
		}
		if p.ClaimedDeviceID != nil {
			registered++
		} else {
			unregistered = append(unregistered, p)
		}
	}

	h.tmpl.Render(w, "admin_dashboard.html", map[string]interface{}{
		"Participants": participants,
		"Ranking":      ranking,
		"Setting":      setting,
		"Total":        total,
		"Registered":   registered,
		"Unregistered": unregistered,
	})
}

func (h *AdminHandler) handleParticipants(w http.ResponseWriter, r *http.Request) {
	participants, _ := h.store.ListParticipants()
	setting, _ := h.store.GetSetting()
	h.tmpl.Render(w, "admin_participants.html", map[string]interface{}{
		"Participants": participants,
		"BaseURL":      setting.BaseURL,
	})
}

func (h *AdminHandler) handleNewParticipant(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		h.tmpl.Render(w, "admin_participant_form.html", map[string]interface{}{"IsNew": true})
		return
	}

	if setting, _ := h.store.GetSetting(); setting.RosterLocked {
		h.tmpl.Render(w, "admin_participant_form.html", map[string]interface{}{
			"IsNew": true, "Error": "Elenco travado — destrave nas configurações para adicionar participantes.",
		})
		return
	}

	r.ParseMultipartForm(10 << 20)
	name := strings.TrimSpace(r.FormValue("name"))
	nickname := strings.TrimSpace(r.FormValue("nickname"))
	if name == "" {
		h.tmpl.Render(w, "admin_participant_form.html", map[string]interface{}{
			"IsNew": true, "Error": "Nome obrigatório",
		})
		return
	}

	photoPath := ""
	if file, header, ferr := r.FormFile("photo"); ferr == nil {
		defer file.Close()
		if data, rerr := io.ReadAll(file); rerr == nil {
			photoPath = h.saveImage(data, header.Filename)
		}
	}

	_, err := h.store.CreateParticipant(name, nickname, photoPath)
	if err != nil {
		h.tmpl.Render(w, "admin_participant_form.html", map[string]interface{}{
			"IsNew": true, "Error": "Erro ao criar: " + err.Error(),
		})
		return
	}

	h.broadcastRanking()
	http.Redirect(w, r, "/admin/participants", http.StatusSeeOther)
}

func (h *AdminHandler) handleEditParticipant(w http.ResponseWriter, r *http.Request) {
	idStr := extractID(r.URL.Path, "/admin/participants/", "/edit")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	p, err := h.store.GetParticipantByID(id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	if r.Method == http.MethodGet {
		h.tmpl.Render(w, "admin_participant_form.html", map[string]interface{}{
			"IsNew":       false,
			"Participant": p,
		})
		return
	}

	r.ParseMultipartForm(10 << 20)
	p.Name = strings.TrimSpace(r.FormValue("name"))
	p.Nickname = strings.TrimSpace(r.FormValue("nickname"))
	p.Active = r.FormValue("active") == "on"

	if file, header, ferr := r.FormFile("photo"); ferr == nil {
		defer file.Close()
		if data, rerr := io.ReadAll(file); rerr == nil {
			if pp := h.saveImage(data, header.Filename); pp != "" {
				p.PhotoPath = pp
			}
		}
	}

	if err := h.store.UpdateParticipant(p); err != nil {
		h.tmpl.Render(w, "admin_participant_form.html", map[string]interface{}{
			"IsNew": false, "Participant": p, "Error": "Erro ao atualizar: " + err.Error(),
		})
		return
	}

	h.broadcastRanking()
	http.Redirect(w, r, "/admin/participants", http.StatusSeeOther)
}

func (h *AdminHandler) handleDeleteParticipant(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	idStr := extractID(r.URL.Path, "/admin/participants/", "/delete")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	if err := h.store.SetParticipantActive(id, false); err != nil {
		http.Error(w, "Erro ao desativar: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.broadcastRanking()
	http.Redirect(w, r, "/admin/participants", http.StatusSeeOther)
}

// saveImage processa (redimensiona/comprime) e grava a imagem em uploads/.
// Retorna o caminho web ("/uploads/..."), ou "" em caso de falha.
func (h *AdminHandler) saveImage(data []byte, origFilename string) string {
	processed, ext := processImage(data, filepath.Ext(origFilename))
	filename := fmt.Sprintf("%d%s", time.Now().UnixNano(), ext)
	if err := os.WriteFile(filepath.Join(h.uploadsDir, filename), processed, 0644); err != nil {
		return ""
	}
	return "/uploads/" + filename
}

func (h *AdminHandler) handleBulkImport(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		h.tmpl.Render(w, "admin_bulk.html", nil)
		return
	}

	if setting, _ := h.store.GetSetting(); setting.RosterLocked {
		h.tmpl.Render(w, "admin_bulk.html", map[string]interface{}{
			"Error": "Elenco travado — destrave nas configurações para importar participantes.",
		})
		return
	}

	r.ParseMultipartForm(50 << 20)

	// Parse CSV
	csvFile, _, err := r.FormFile("csv")
	if err != nil {
		h.tmpl.Render(w, "admin_bulk.html", map[string]interface{}{"Error": "CSV obrigatório"})
		return
	}
	defer csvFile.Close()

	reader := csv.NewReader(csvFile)
	reader.TrimLeadingSpace = true
	records, err := reader.ReadAll()
	if err != nil {
		h.tmpl.Render(w, "admin_bulk.html", map[string]interface{}{"Error": "Erro ao ler CSV: " + err.Error()})
		return
	}

	// Build image name -> bytes map from uploaded files
	images := map[string][]byte{}
	for _, fh := range r.MultipartForm.File["images"] {
		f, err := fh.Open()
		if err != nil {
			continue
		}
		data, _ := io.ReadAll(f)
		f.Close()
		images[fh.Filename] = data
	}

	var created, skipped int
	var errors []string

	for i, rec := range records {
		if i == 0 {
			// Skip header if it says "name"
			if len(rec) > 0 && strings.ToLower(strings.TrimSpace(rec[0])) == "name" {
				continue
			}
		}
		if len(rec) < 1 {
			continue
		}
		name := strings.TrimSpace(rec[0])
		nickname := ""
		if len(rec) >= 2 {
			nickname = strings.TrimSpace(rec[1])
		}
		imageFile := ""
		if len(rec) >= 3 {
			imageFile = strings.TrimSpace(rec[2])
		}

		if name == "" {
			skipped++
			continue
		}

		photoPath := ""
		if imageFile != "" {
			if data, ok := images[imageFile]; ok {
				photoPath = h.saveImage(data, imageFile)
			}
		}

		_, err := h.store.CreateParticipant(name, nickname, photoPath)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", name, err))
			skipped++
		} else {
			created++
		}
	}

	h.broadcastRanking()
	h.tmpl.Render(w, "admin_bulk.html", map[string]interface{}{
		"Created": created,
		"Skipped": skipped,
		"Errors":  errors,
		"Done":    true,
	})
}

func (h *AdminHandler) handleQRSheet(w http.ResponseWriter, r *http.Request) {
	setting, _ := h.store.GetSetting()
	participants, _ := h.store.ListActiveParticipants()

	type qrEntry struct {
		Participant *model.Participant
		QRBase64    string
		URL         string
	}

	var entries []qrEntry
	for _, p := range participants {
		stickerURL := qr.StickerURL(setting.BaseURL, p.Token)
		b64, _ := generateQRBase64(stickerURL)
		entries = append(entries, qrEntry{
			Participant: p,
			QRBase64:    b64,
			URL:         stickerURL,
		})
	}

	h.tmpl.Render(w, "admin_qrsheet.html", map[string]interface{}{
		"Entries": entries,
	})
}

func (h *AdminHandler) handleQRImage(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.URL.Path, "/admin/qr/")
	setting, _ := h.store.GetSetting()
	stickerURL := qr.StickerURL(setting.BaseURL, token)

	png, err := qr.GeneratePNG(stickerURL, 256)
	if err != nil {
		http.Error(w, "QR error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Write(png) //nolint:errcheck
}

func (h *AdminHandler) handleSettings(w http.ResponseWriter, r *http.Request) {
	setting, _ := h.store.GetSetting()

	if r.Method == http.MethodGet {
		var kickoffStr string
		if setting.KickoffAt != nil {
			kickoffStr = setting.KickoffAt.Local().Format("2006-01-02T15:04")
		}
		h.tmpl.Render(w, "admin_settings.html", map[string]interface{}{
			"Setting":   setting,
			"KickoffAt": kickoffStr,
			"Saved":     r.URL.Query().Get("saved") == "1",
		})
		return
	}

	r.ParseForm()
	setting.BaseURL = strings.TrimRight(strings.TrimSpace(r.FormValue("base_url")), "/")
	kickoffStr := r.FormValue("kickoff_at")
	if kickoffStr != "" {
		t, err := time.ParseInLocation("2006-01-02T15:04", kickoffStr, time.Local)
		if err == nil {
			setting.KickoffAt = &t
		}
	} else {
		setting.KickoffAt = nil
	}

	newPassword := r.FormValue("new_password")
	if newPassword != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
		if err == nil {
			setting.AdminPasswordHash = string(hash)
		}
	}

	if err := h.store.SaveSetting(setting); err != nil {
		h.tmpl.Render(w, "admin_settings.html", map[string]interface{}{
			"Setting": setting, "Error": "Erro ao salvar: " + err.Error(),
		})
		return
	}

	http.Redirect(w, r, "/admin/settings?saved=1", http.StatusSeeOther)
}

func (h *AdminHandler) handleLockRoster(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	setting, _ := h.store.GetSetting()
	r.ParseForm()
	setting.RosterLocked = r.FormValue("lock") == "1"
	h.store.SaveSetting(setting) //nolint:errcheck

	// Broadcast roster lock event
	data, _ := json.Marshal(map[string]interface{}{
		"locked": setting.RosterLocked,
	})
	h.hub.Broadcast("roster", string(data))

	http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
}

func (h *AdminHandler) handleBackup(w http.ResponseWriter, r *http.Request) {
	// Gera um snapshot consistente (VACUUM INTO) em arquivo temporario e o envia.
	tmp := filepath.Join(h.dataDir, fmt.Sprintf("copa-download-%d.db", time.Now().UnixNano()))
	if err := h.store.BackupTo(tmp); err != nil {
		http.Error(w, "Erro ao gerar backup: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer os.Remove(tmp)

	f, err := os.Open(tmp)
	if err != nil {
		http.Error(w, "Erro ao abrir backup: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="copa-backup-%s.db"`, time.Now().Format("20060102-150405")))
	io.Copy(w, f) //nolint:errcheck
}

// handleExport baixa o resultado em CSV. Apos o apito, exporta a classificacao
// congelada (oficial); antes, a classificacao ao vivo.
func (h *AdminHandler) handleExport(w http.ResponseWriter, r *http.Request) {
	setting, _ := h.store.GetSetting()
	var ranking []*model.RankEntry
	frozen := setting.KickoffAt != nil && !time.Now().Before(*setting.KickoffAt)
	if frozen {
		ranking, _ = h.store.GetFinalRanking(*setting.KickoffAt)
	} else {
		ranking, _ = h.store.GetRanking()
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="resultado-copa-%s.csv"`, time.Now().Format("20060102-150405")))
	w.Write([]byte{0xEF, 0xBB, 0xBF}) // BOM UTF-8 (Excel reconhece acentos)

	cw := csv.NewWriter(w)
	defer cw.Flush()
	cw.Write([]string{"posicao", "nome", "apelido", "figurinhas", "total", "completo", "horario_ultima"}) //nolint:errcheck
	for i, e := range ranking {
		completo := "nao"
		if e.Complete {
			completo = "sim"
		}
		horario := ""
		if e.MaxReachedAt != nil {
			horario = e.MaxReachedAt.Local().Format("2006-01-02 15:04:05")
		}
		cw.Write([]string{ //nolint:errcheck
			strconv.Itoa(i + 1),
			e.Name,
			e.Nickname,
			strconv.Itoa(e.Count),
			strconv.Itoa(e.Total),
			completo,
			horario,
		})
	}
}

func (h *AdminHandler) handleTransfer(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		participants, _ := h.store.ListParticipants()
		h.tmpl.Render(w, "admin_transfer.html", map[string]interface{}{
			"Participants": participants,
		})
		return
	}

	r.ParseForm()
	srcIDStr := r.FormValue("src_id")
	dstIDStr := r.FormValue("dst_id")
	srcID, err1 := strconv.ParseInt(srcIDStr, 10, 64)
	dstID, err2 := strconv.ParseInt(dstIDStr, 10, 64)
	if err1 != nil || err2 != nil || srcID == dstID {
		h.tmpl.Render(w, "admin_transfer.html", map[string]interface{}{
			"Error": "IDs inválidos ou iguais",
		})
		return
	}

	if err := h.store.TransferCollection(srcID, dstID); err != nil {
		h.tmpl.Render(w, "admin_transfer.html", map[string]interface{}{
			"Error": "Erro ao transferir: " + err.Error(),
		})
		return
	}

	h.broadcastRanking()
	http.Redirect(w, r, "/admin/dashboard?transferred=1", http.StatusSeeOther)
}

// ---- Cartões para impressão ----

func (h *AdminHandler) handleCards(w http.ResponseWriter, r *http.Request) {
	setting, _ := h.store.GetSetting()
	participants, _ := h.store.ListActiveParticipants()

	type cardEntry struct {
		Participant *model.Participant
		QRBase64    string
		URL         string
	}
	var entries []cardEntry
	for _, p := range participants {
		stickerURL := qr.StickerURL(setting.BaseURL, p.Token)
		b64, _ := generateQRBase64(stickerURL)
		entries = append(entries, cardEntry{Participant: p, QRBase64: b64, URL: stickerURL})
	}

	h.tmpl.Render(w, "admin_cards.html", map[string]interface{}{
		"Entries": entries,
		"BaseURL": setting.BaseURL,
	})
}

// ---- Sistema: diagnóstico de rede + reset + restaurar ----

// localIPv4s retorna os endereços IPv4 não-loopback das interfaces ativas.
func localIPv4s() []string {
	var out []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			if v4 := ip.To4(); v4 != nil {
				out = append(out, v4.String())
			}
		}
	}
	return out
}

func (h *AdminHandler) handleSystem(w http.ResponseWriter, r *http.Request) {
	setting, _ := h.store.GetSetting()

	// Porta a partir do endereço de escuta (":8080" -> "8080").
	port := strings.TrimPrefix(h.listenAddr, ":")
	if i := strings.LastIndex(h.listenAddr, ":"); i >= 0 {
		port = h.listenAddr[i+1:]
	}

	ips := localIPv4s()

	type ipEntry struct {
		IP       string
		URL      string
		QRBase64 string
	}
	var entries []ipEntry
	for _, ip := range ips {
		u := fmt.Sprintf("http://%s:%s", ip, port)
		b64, _ := generateQRBase64(u)
		entries = append(entries, ipEntry{IP: ip, URL: u, QRBase64: b64})
	}

	// O base_url salvo bate com algum IP local?
	matches := false
	if host := hostOf(setting.BaseURL); host != "" {
		for _, ip := range ips {
			if host == ip {
				matches = true
				break
			}
		}
		// localhost/127.0.0.1 é válido para uso na própria máquina.
		if host == "localhost" || host == "127.0.0.1" {
			matches = true
		}
	}

	h.tmpl.Render(w, "admin_system.html", map[string]interface{}{
		"Setting":           setting,
		"BaseURL":           setting.BaseURL,
		"BaseURLHost":       hostOf(setting.BaseURL),
		"IPs":               entries,
		"Port":              port,
		"BaseURLMatchesLAN": matches,
		"Reset":             r.URL.Query().Get("reset") == "1",
		"Restored":          r.URL.Query().Get("restored") == "1",
	})
}

func (h *AdminHandler) handleReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.ParseForm()
	if strings.TrimSpace(r.FormValue("confirm")) != "LIMPAR" {
		h.handleSystemWithError(w, r, "Para limpar, digite LIMPAR no campo de confirmação.")
		return
	}

	if err := h.store.ResetGameData(); err != nil {
		h.handleSystemWithError(w, r, "Erro ao limpar dados: "+err.Error())
		return
	}

	// Remove as fotos do ensaio (mantém a pasta).
	if entries, err := os.ReadDir(h.uploadsDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				os.Remove(filepath.Join(h.uploadsDir, e.Name())) //nolint:errcheck
			}
		}
	}

	h.broadcastRanking()
	http.Redirect(w, r, "/admin/sistema?reset=1", http.StatusSeeOther)
}

func (h *AdminHandler) handleRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(100 << 20); err != nil {
		h.handleSystemWithError(w, r, "Erro ao ler upload: "+err.Error())
		return
	}
	file, _, ferr := r.FormFile("backup")
	if ferr != nil {
		h.handleSystemWithError(w, r, "Selecione um arquivo de backup (.db).")
		return
	}
	defer file.Close()

	tmp := filepath.Join(h.dataDir, fmt.Sprintf("copa-restore-%d.db", time.Now().UnixNano()))
	dst, err := os.Create(tmp)
	if err != nil {
		h.handleSystemWithError(w, r, "Erro ao salvar upload: "+err.Error())
		return
	}
	_, cerr := io.Copy(dst, file)
	dst.Close()
	defer os.Remove(tmp)
	if cerr != nil {
		h.handleSystemWithError(w, r, "Erro ao salvar upload: "+cerr.Error())
		return
	}

	if err := h.store.RestoreFrom(tmp); err != nil {
		h.handleSystemWithError(w, r, "Erro ao restaurar: "+err.Error())
		return
	}

	h.broadcastRanking()
	http.Redirect(w, r, "/admin/sistema?restored=1", http.StatusSeeOther)
}

// handleSystemWithError re-renderiza a página de sistema com uma mensagem de erro.
func (h *AdminHandler) handleSystemWithError(w http.ResponseWriter, r *http.Request, msg string) {
	setting, _ := h.store.GetSetting()
	h.tmpl.Render(w, "admin_system.html", map[string]interface{}{
		"Setting": setting,
		"BaseURL": setting.BaseURL,
		"Error":   msg,
	})
}

// hostOf extrai o host de uma URL (sem esquema/porta), ou "" se inválida.
func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Hostname()
}

func (h *AdminHandler) broadcastRanking() { h.notifier.Ranking() }

func extractID(path, prefix, suffix string) string {
	s := strings.TrimPrefix(path, prefix)
	s = strings.TrimSuffix(s, suffix)
	return s
}

func generateQRBase64(u string) (string, error) {
	return qr.GenerateBase64(u, 256)
}

func renderError(w http.ResponseWriter, tmpl *Templates, msg string, code int) {
	w.WriteHeader(code)
	tmpl.Render(w, "error.html", map[string]interface{}{"Message": msg})
}
