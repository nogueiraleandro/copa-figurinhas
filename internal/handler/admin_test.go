package handler

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	idb "copa/internal/db"
	"copa/internal/service"
	"copa/internal/sse"
)

func newAdminTestServer(t *testing.T) (*httptest.Server, *service.Store) {
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
	adminH := NewAdminHandler(store, hub, tmpl, t.TempDir(), t.TempDir(), ":8080", notifier)

	mux := http.NewServeMux()
	mux.Handle("/admin", adminH)
	mux.Handle("/admin/", adminH)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, store
}

func adminClient(t *testing.T) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func loginAdmin(t *testing.T, srv *httptest.Server, client *http.Client, password string) {
	t.Helper()
	resp, err := client.PostForm(srv.URL+"/admin", url.Values{"password": {password}})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login esperava 303, got %d", resp.StatusCode)
	}
}

// Rotas protegidas exigem auth.
func TestAdminAuthRequired(t *testing.T) {
	srv, _ := newAdminTestServer(t)
	client := adminClient(t)
	resp, err := client.Get(srv.URL + "/admin/dashboard")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("sem login esperava redirect 303, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/admin" {
		t.Fatalf("deveria redirecionar pro login, got %q", loc)
	}
}

// 1o login define a senha; senha errada depois e rejeitada.
func TestAdminLoginFlow(t *testing.T) {
	srv, _ := newAdminTestServer(t)
	client := adminClient(t)
	loginAdmin(t, srv, client, "segredo123")

	// Agora autenticado: dashboard responde 200.
	resp, err := client.Get(srv.URL + "/admin/dashboard")
	if err != nil {
		t.Fatalf("dashboard: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dashboard autenticado esperava 200, got %d", resp.StatusCode)
	}

	// Cliente novo com senha errada -> tela de login com erro.
	other := adminClient(t)
	resp, err = other.PostForm(srv.URL+"/admin", url.Values{"password": {"errada"}})
	if err != nil {
		t.Fatalf("login errado: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "Senha incorreta") {
		t.Fatalf("senha errada deveria mostrar erro, status=%d", resp.StatusCode)
	}
}

// 1o login com senha vazia e recusado (nao cria admin sem senha por engano).
func TestAdminFirstLoginRejectsEmptyPassword(t *testing.T) {
	srv, store := newAdminTestServer(t)
	client := adminClient(t)

	resp, err := client.PostForm(srv.URL+"/admin", url.Values{"password": {"   "}})
	if err != nil {
		t.Fatalf("login vazio: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "não vazia") {
		t.Fatalf("senha vazia deveria mostrar erro, status=%d", resp.StatusCode)
	}
	// Nenhum hash deve ter sido salvo.
	if setting, _ := store.GetSetting(); setting.AdminPasswordHash != "" {
		t.Fatalf("senha vazia nao deveria definir hash de admin")
	}

	// Depois disso, um 1o login valido ainda funciona.
	loginAdmin(t, srv, client, "segredo123")
	if setting, _ := store.GetSetting(); setting.AdminPasswordHash == "" {
		t.Fatalf("login valido deveria definir hash de admin")
	}
}

// Import em massa: CSV (nome,apelido,imagem) + arquivos de imagem.
func TestAdminBulkImport(t *testing.T) {
	srv, store := newAdminTestServer(t)
	client := adminClient(t)
	loginAdmin(t, srv, client, "senha")

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	csvPart, _ := mw.CreateFormFile("csv", "pessoas.csv")
	csvPart.Write([]byte("name,nickname,image\nLeandro,Leo,leandro.png\nAna Beatriz,Aninha,\n"))

	imgPart, _ := mw.CreateFormFile("images", "leandro.png")
	imgPart.Write(onePixelPNG())

	mw.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/admin/bulk", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("bulk: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bulk esperava 200, got %d", resp.StatusCode)
	}

	people, _ := store.ListParticipants()
	if len(people) != 2 {
		t.Fatalf("esperava 2 participantes importados, got %d", len(people))
	}
	// Leandro deve ter foto; Ana (sem imagem) nao.
	byName := map[string]string{}
	for _, p := range people {
		byName[p.Name] = p.PhotoPath
	}
	if byName["Leandro"] == "" {
		t.Errorf("Leandro deveria ter foto importada")
	}
	if byName["Ana Beatriz"] != "" {
		t.Errorf("Ana Beatriz nao tinha imagem no CSV, nao deveria ter foto")
	}
}

// Salvar configuracoes persiste base_url.
func TestAdminSettingsSaved(t *testing.T) {
	srv, store := newAdminTestServer(t)
	client := adminClient(t)
	loginAdmin(t, srv, client, "senha")

	resp, err := client.PostForm(srv.URL+"/admin/settings", url.Values{
		"base_url": {"http://192.168.0.50:8080/"},
	})
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	resp.Body.Close()

	set, _ := store.GetSetting()
	if set.BaseURL != "http://192.168.0.50:8080" {
		t.Fatalf("base_url nao salvou (esperava sem barra final), got %q", set.BaseURL)
	}
}

func TestAdminAISettingsSaved(t *testing.T) {
	srv, store := newAdminTestServer(t)
	client := adminClient(t)
	loginAdmin(t, srv, client, "senha")

	resp, err := client.PostForm(srv.URL+"/admin/settings", url.Values{
		"base_url":       {"http://localhost:8080"},
		"gemini_api_key": {"key-123"},
		"ai_model":       {"gemini-2.5-flash-image"},
		"ai_prompt":      {"usar estilo copa"},
	})
	if err != nil {
		t.Fatalf("settings ai: %v", err)
	}
	resp.Body.Close()

	set, _ := store.GetSetting()
	if set.GeminiAPIKey != "key-123" || set.AIModel != "gemini-2.5-flash-image" || set.AIPrompt != "usar estilo copa" {
		t.Fatalf("settings de IA nao salvaram: %#v", set)
	}
}

func TestAdminAISettingsRejectsTextModel(t *testing.T) {
	srv, store := newAdminTestServer(t)
	client := adminClient(t)
	loginAdmin(t, srv, client, "senha")

	resp, err := client.PostForm(srv.URL+"/admin/settings", url.Values{
		"base_url": {"http://localhost:8080"},
		"ai_model": {"gemini-3.1-pro-preview"},
	})
	if err != nil {
		t.Fatalf("settings ai model: %v", err)
	}
	resp.Body.Close()

	set, _ := store.GetSetting()
	if set.AIModel != "gemini-3.1-flash-image-preview" {
		t.Fatalf("modelo sem image deveria voltar ao default, got %q", set.AIModel)
	}
}

func TestAdminUseAIWithoutKeyFallsBackToOriginalPhoto(t *testing.T) {
	srv, store := newAdminTestServer(t)
	client := adminClient(t)
	loginAdmin(t, srv, client, "senha")

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("name", "Sem Chave") //nolint:errcheck
	mw.WriteField("use_ai", "on")      //nolint:errcheck
	imgPart, _ := mw.CreateFormFile("photo", "foto.png")
	imgPart.Write(onePixelPNG()) //nolint:errcheck
	mw.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/admin/participants/new", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("new participant use_ai: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("cadastro esperava 303, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/admin/participants" {
		t.Fatalf("sem chave deve ser no-op sem warning, got loc=%q", loc)
	}
	people, _ := store.ListParticipants()
	if len(people) != 1 || people[0].PhotoPath == "" || !strings.HasSuffix(people[0].PhotoPath, ".png") {
		t.Fatalf("foto original deveria ser salva como png, got %#v", people)
	}
}

func TestAdminOriginalAndReadyStickerUploadsUpdateProductionStatus(t *testing.T) {
	srv, store := newAdminTestServer(t)
	client := adminClient(t)
	loginAdmin(t, srv, client, "senha")

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("name", "Com Foto") //nolint:errcheck
	orig, _ := mw.CreateFormFile("original_photo", "original.png")
	orig.Write(onePixelPNG()) //nolint:errcheck
	mw.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/admin/participants/new", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("new original upload: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("cadastro esperava 303, got %d", resp.StatusCode)
	}
	people, _ := store.ListParticipants()
	if len(people) != 1 {
		t.Fatalf("esperava 1 participante, got %d", len(people))
	}
	if people[0].PhotoPath == "" || people[0].StickerPath != "" || people[0].ProductionStatus != "photo_received" {
		t.Fatalf("foto original deveria marcar foto_recebida sem figurinha pronta: %#v", people[0])
	}

	buf.Reset()
	mw = multipart.NewWriter(&buf)
	mw.WriteField("name", "Com Foto") //nolint:errcheck
	mw.WriteField("active", "on")     //nolint:errcheck
	ready, _ := mw.CreateFormFile("sticker", "pronta.png")
	ready.Write(onePixelPNG()) //nolint:errcheck
	mw.Close()
	req, _ = http.NewRequest("POST", srv.URL+"/admin/participants/"+itoa(people[0].ID)+"/edit", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("edit ready upload: %v", err)
	}
	resp.Body.Close()
	updated, _ := store.GetParticipantByID(people[0].ID)
	if updated.StickerPath == "" || updated.ProductionStatus != "sticker_done" {
		t.Fatalf("figurinha pronta deveria marcar feita: %#v", updated)
	}
}

func TestAdminProductionPageImportAndFilters(t *testing.T) {
	srv, store := newAdminTestServer(t)
	client := adminClient(t)
	loginAdmin(t, srv, client, "senha")

	resp, err := client.Get(srv.URL + "/admin/production")
	if err != nil {
		t.Fatalf("production get: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "Produ") {
		t.Fatalf("production deveria renderizar, status=%d", resp.StatusCode)
	}

	resp, err = client.PostForm(srv.URL+"/admin/production/import-initial", url.Values{})
	if err != nil {
		t.Fatalf("import initial: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("import inicial esperava 303, got %d", resp.StatusCode)
	}
	people, _ := store.ListParticipants()
	if len(people) < 70 {
		t.Fatalf("lista inicial deveria dividir pessoas e especiais, got %d", len(people))
	}
	var bia, vinicius, special int
	for _, p := range people {
		if p.Name == "Bia" {
			bia++
		}
		if p.Name == "Vinicius" {
			vinicius++
		}
		if p.Category == "special" {
			special++
		}
	}
	if bia != 1 || vinicius != 1 || special == 0 {
		t.Fatalf("import deveria dividir Bia/Vinicius e criar especiais: bia=%d vini=%d special=%d", bia, vinicius, special)
	}

	if err := store.SetParticipantActive(people[0].ID, false); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	resp, err = client.Get(srv.URL + "/admin/participants")
	if err != nil {
		t.Fatalf("participants: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), people[0].Name) {
		t.Fatalf("participantes desativados deveriam ficar ocultos por padrao")
	}
	resp, err = client.Get(srv.URL + "/admin/participants?show_inactive=1")
	if err != nil {
		t.Fatalf("participants inactive: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), people[0].Name) {
		t.Fatalf("show_inactive=1 deveria mostrar desativado")
	}
}

// Travar elenco bloqueia adicionar novos participantes (correcao de bug).
func TestAdminRosterLockBlocksAdd(t *testing.T) {
	srv, store := newAdminTestServer(t)
	client := adminClient(t)
	loginAdmin(t, srv, client, "senha")

	// Trava o elenco.
	resp, err := client.PostForm(srv.URL+"/admin/lock", url.Values{"lock": {"1"}})
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	resp.Body.Close()
	if set, _ := store.GetSetting(); !set.RosterLocked {
		t.Fatal("elenco deveria estar travado")
	}

	// Tentar adicionar com elenco travado -> recusado, nada criado.
	resp, err = client.PostForm(srv.URL+"/admin/participants/new", url.Values{"name": {"Atrasado"}})
	if err != nil {
		t.Fatalf("new participant: %v", err)
	}
	resp.Body.Close()

	if n, _ := store.CountActiveParticipants(); n != 0 {
		t.Fatalf("com elenco travado nao deveria criar participante, got %d", n)
	}
}

// Seguranca: cookie de admin forjado ("ok") nao da acesso (precisa de sessao valida).
func TestAdminForgedCookieRejected(t *testing.T) {
	srv, _ := newAdminTestServer(t)
	client := adminClient(t)

	req, _ := http.NewRequest("GET", srv.URL+"/admin/dashboard", nil)
	req.AddCookie(&http.Cookie{Name: "copa_admin", Value: "ok"})
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin" {
		t.Fatalf("cookie forjado deveria ser rejeitado (redirect /admin), got status=%d loc=%q",
			resp.StatusCode, resp.Header.Get("Location"))
	}
}

// Backup gera um arquivo SQLite valido para download.
func TestAdminBackupDownloads(t *testing.T) {
	srv, store := newAdminTestServer(t)
	store.CreateParticipant("Dado", "", "") //nolint:errcheck
	client := adminClient(t)
	loginAdmin(t, srv, client, "senha")

	resp, err := client.Get(srv.URL + "/admin/backup")
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("backup esperava 200, got %d", resp.StatusCode)
	}
	if !strings.HasPrefix(string(body), "SQLite format 3") {
		t.Fatalf("download nao parece um arquivo SQLite valido")
	}
}

func TestAdminFullBackupDownloadsZip(t *testing.T) {
	srv, store := newAdminTestServer(t)
	store.CreateParticipant("Dado", "", "") //nolint:errcheck
	client := adminClient(t)
	loginAdmin(t, srv, client, "senha")

	resp, err := client.Get(srv.URL + "/admin/backup/full")
	if err != nil {
		t.Fatalf("full backup: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("full backup esperava 200, got %d", resp.StatusCode)
	}

	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("full backup deveria ser zip valido: %v", err)
	}
	files := map[string]bool{}
	for _, f := range zr.File {
		files[f.Name] = true
	}
	for _, want := range []string{"data/copa.db", "manifest.txt"} {
		if !files[want] {
			t.Fatalf("zip deveria conter %s; arquivos=%v", want, files)
		}
	}
}

// Export CSV traz cabecalho e uma linha por participante registrado.
func TestAdminExportCSV(t *testing.T) {
	srv, store := newAdminTestServer(t)
	a, _ := store.CreateParticipant("Alice", "Ali", "")
	b, _ := store.CreateParticipant("Bob", "", "")
	store.CreateDevice(a.ID)          //nolint:errcheck
	store.CreateDevice(b.ID)          //nolint:errcheck
	store.AddToCollection(a.ID, a.ID) //nolint:errcheck
	store.AddToCollection(a.ID, b.ID) //nolint:errcheck
	store.AddToCollection(b.ID, b.ID) //nolint:errcheck

	client := adminClient(t)
	loginAdmin(t, srv, client, "senha")

	resp, err := client.Get(srv.URL + "/admin/export")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export esperava 200, got %d", resp.StatusCode)
	}
	csv := string(body)
	if !strings.Contains(csv, "posicao,nome,apelido") {
		t.Fatalf("CSV deveria ter cabecalho, got: %q", csv[:min(60, len(csv))])
	}
	if !strings.Contains(csv, "Alice") || !strings.Contains(csv, "Bob") {
		t.Fatalf("CSV deveria conter os participantes")
	}
}

// Dashboard mostra quem ainda nao entrou.
func TestAdminDashboardShowsPending(t *testing.T) {
	srv, store := newAdminTestServer(t)
	entrou, _ := store.CreateParticipant("JaEntrou", "", "")
	store.CreateParticipant("AindaNao", "", "") //nolint:errcheck
	store.CreateDevice(entrou.ID)               //nolint:errcheck

	client := adminClient(t)
	loginAdmin(t, srv, client, "senha")
	resp, err := client.Get(srv.URL + "/admin/dashboard")
	if err != nil {
		t.Fatalf("dashboard: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "AindaNao") {
		t.Fatalf("dashboard deveria listar quem ainda nao entrou")
	}
}

// Folha de QR renderiza com a URL de cada participante.
func TestAdminQRSheetRenders(t *testing.T) {
	srv, store := newAdminTestServer(t)
	p, _ := store.CreateParticipant("Carla", "", "")
	client := adminClient(t)
	loginAdmin(t, srv, client, "senha")

	resp, err := client.Get(srv.URL + "/admin/qrsheet")
	if err != nil {
		t.Fatalf("qrsheet: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("qrsheet esperava 200, got %d", resp.StatusCode)
	}
	html := string(body)
	if !strings.Contains(html, p.Name) {
		t.Fatalf("folha de QR deveria conter o nome do participante (%q)", p.Name)
	}
	if !strings.Contains(html, "data:image/png;base64,") {
		t.Fatalf("folha de QR deveria conter o QR embutido (data:image/png;base64,)")
	}
}

// Página de sistema renderiza e mostra o base_url atual.
func TestAdminSystemPageRenders(t *testing.T) {
	srv, store := newAdminTestServer(t)
	set, _ := store.GetSetting()
	set.BaseURL = "http://10.0.0.7:8080"
	store.SaveSetting(set) //nolint:errcheck

	client := adminClient(t)
	loginAdmin(t, srv, client, "senha")

	resp, err := client.Get(srv.URL + "/admin/sistema")
	if err != nil {
		t.Fatalf("sistema: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sistema esperava 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "10.0.0.7:8080") {
		t.Fatalf("página de sistema deveria mostrar o base_url configurado")
	}
}

// Preflight renderiza o checklist operacional.
func TestAdminPreflightPageRenders(t *testing.T) {
	srv, store := newAdminTestServer(t)
	store.CreateParticipant("Dado", "", "") //nolint:errcheck
	client := adminClient(t)
	loginAdmin(t, srv, client, "senha")

	resp, err := client.Get(srv.URL + "/admin/preflight")
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("preflight esperava 200, got %d", resp.StatusCode)
	}
	html := string(body)
	if !strings.Contains(html, "Pre-voo do evento") || !strings.Contains(html, "Checklist operacional") {
		t.Fatalf("preflight deveria renderizar o checklist")
	}
}

// Reset exige a confirmação "LIMPAR"; sem ela não apaga, com ela apaga tudo.
func TestAdminResetRequiresConfirmation(t *testing.T) {
	srv, store := newAdminTestServer(t)
	store.CreateParticipant("Dado", "", "") //nolint:errcheck
	client := adminClient(t)
	loginAdmin(t, srv, client, "senha")

	// Sem a palavra certa -> não apaga.
	resp, err := client.PostForm(srv.URL+"/admin/sistema/reset", url.Values{"confirm": {"errado"}})
	if err != nil {
		t.Fatalf("reset errado: %v", err)
	}
	resp.Body.Close()
	if n, _ := store.CountActiveParticipants(); n != 1 {
		t.Fatalf("sem confirmação não deveria apagar, got %d", n)
	}

	// Com "LIMPAR" -> apaga.
	resp, err = client.PostForm(srv.URL+"/admin/sistema/reset", url.Values{"confirm": {"LIMPAR"}})
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	resp.Body.Close()
	if n, _ := store.CountActiveParticipants(); n != 0 {
		t.Fatalf("com LIMPAR deveria apagar tudo, got %d", n)
	}
}

// Restaurar backup via upload repõe os dados.
func TestAdminRestoreUpload(t *testing.T) {
	srv, store := newAdminTestServer(t)
	client := adminClient(t)
	loginAdmin(t, srv, client, "senha")

	// Gera um backup com 1 participante via download.
	store.CreateParticipant("Restaurado", "", "") //nolint:errcheck
	resp, err := client.Get(srv.URL + "/admin/backup")
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	backup, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	// Apaga e confirma vazio.
	store.ResetGameData() //nolint:errcheck
	if n, _ := store.CountActiveParticipants(); n != 0 {
		t.Fatalf("deveria estar vazio antes do restore, got %d", n)
	}

	// Faz upload do backup.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("backup", "backup.db")
	part.Write(backup)
	mw.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/admin/sistema/restore", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	resp.Body.Close()

	people, _ := store.ListParticipants()
	if len(people) != 1 || people[0].Name != "Restaurado" {
		t.Fatalf("restore deveria repor o participante, got %d", len(people))
	}
}

// Restaurar arquivo inválido -> erro amigável (não derruba o servidor).
func TestAdminRestoreInvalidFile(t *testing.T) {
	srv, _ := newAdminTestServer(t)
	client := adminClient(t)
	loginAdmin(t, srv, client, "senha")

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("backup", "naoedb.db")
	part.Write([]byte("isso nao e um banco sqlite"))
	mw.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/admin/sistema/restore", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("restore inválido: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "restaurar") {
		t.Fatalf("arquivo inválido deveria mostrar erro amigável, status=%d", resp.StatusCode)
	}
}

// Página de figurinhas (frente) renderiza com o nome de cada participante.
func TestAdminCardsRenders(t *testing.T) {
	srv, store := newAdminTestServer(t)
	p, _ := store.CreateParticipant("Carla", "", "")
	client := adminClient(t)
	loginAdmin(t, srv, client, "senha")

	resp, err := client.Get(srv.URL + "/admin/cards")
	if err != nil {
		t.Fatalf("cards: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cards esperava 200, got %d", resp.StatusCode)
	}
	// A página de figurinhas mostra só a imagem (sem nome em texto): valida que ao
	// menos uma folha foi renderizada para o participante criado.
	if !strings.Contains(string(body), `class="sheet"`) {
		t.Fatalf("figurinhas deveriam renderizar ao menos uma folha (.sheet)")
	}
	_ = p
}

// PNG 1x1 valido para upload de teste.
func onePixelPNG() []byte {
	return []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D,
		0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4, 0x89, 0x00, 0x00, 0x00,
		0x0A, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00, 0x00, 0x00, 0x00, 0x49,
		0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82,
	}
}

// Listagem de participantes renderiza a tabela.
func TestAdminParticipantsList(t *testing.T) {
	srv, store := newAdminTestServer(t)
	store.CreateParticipant("Diego", "", "") //nolint:errcheck
	client := adminClient(t)
	loginAdmin(t, srv, client, "senha")

	resp, err := client.Get(srv.URL + "/admin/participants")
	if err != nil {
		t.Fatalf("participants: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("participants esperava 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Diego") {
		t.Fatalf("a listagem deveria conter o participante 'Diego'")
	}
}

// Editar participante via POST atualiza nome e redireciona.
func TestAdminEditParticipant(t *testing.T) {
	srv, store := newAdminTestServer(t)
	p, _ := store.CreateParticipant("Erica", "", "")
	client := adminClient(t)
	loginAdmin(t, srv, client, "senha")

	// GET do formulario de edicao.
	resp, err := client.Get(srv.URL + "/admin/participants/" + itoa(p.ID) + "/edit")
	if err != nil {
		t.Fatalf("edit form: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("edit GET esperava 200, got %d", resp.StatusCode)
	}

	// POST com novo nome.
	resp, err = client.PostForm(srv.URL+"/admin/participants/"+itoa(p.ID)+"/edit", url.Values{
		"name":   {"Erica Souza"},
		"active": {"on"},
	})
	if err != nil {
		t.Fatalf("edit post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("edit POST esperava 303, got %d", resp.StatusCode)
	}
	updated, _ := store.GetParticipantByID(p.ID)
	if updated.Name != "Erica Souza" {
		t.Fatalf("nome deveria virar 'Erica Souza', got %q", updated.Name)
	}
}

// Excluir (desativar) participante: GET é 405; POST desativa.
func TestAdminDeleteParticipant(t *testing.T) {
	srv, store := newAdminTestServer(t)
	p, _ := store.CreateParticipant("Fabio", "", "")
	client := adminClient(t)
	loginAdmin(t, srv, client, "senha")

	// GET deve ser rejeitado (405).
	resp, err := client.Get(srv.URL + "/admin/participants/" + itoa(p.ID) + "/delete")
	if err != nil {
		t.Fatalf("delete get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("delete GET esperava 405, got %d", resp.StatusCode)
	}

	// POST desativa.
	resp, err = client.PostForm(srv.URL+"/admin/participants/"+itoa(p.ID)+"/delete", url.Values{})
	if err != nil {
		t.Fatalf("delete post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete POST esperava 303, got %d", resp.StatusCode)
	}
	after, _ := store.GetParticipantByID(p.ID)
	if after.Active {
		t.Fatalf("participante deveria estar inativo apos delete")
	}
}

// Transferência de coleção entre participantes.
func TestAdminTransfer(t *testing.T) {
	srv, store := newAdminTestServer(t)
	src, _ := store.CreateParticipant("Gabi Origem", "", "")
	dst, _ := store.CreateParticipant("Gui Destino", "", "")
	outro, _ := store.CreateParticipant("Outro", "", "")
	store.AddToCollection(src.ID, outro.ID) //nolint:errcheck

	client := adminClient(t)
	loginAdmin(t, srv, client, "senha")

	// GET renderiza a tela de transferencia.
	resp, err := client.Get(srv.URL + "/admin/transfer")
	if err != nil {
		t.Fatalf("transfer get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("transfer GET esperava 200, got %d", resp.StatusCode)
	}

	// POST transfere src -> dst.
	resp, err = client.PostForm(srv.URL+"/admin/transfer", url.Values{
		"src_id": {itoa(src.ID)},
		"dst_id": {itoa(dst.ID)},
	})
	if err != nil {
		t.Fatalf("transfer post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("transfer POST esperava 303, got %d", resp.StatusCode)
	}
	if has, _ := store.HasSticker(dst.ID, outro.ID); !has {
		t.Fatalf("destino deveria receber a figurinha transferida")
	}
}

// QR image em PNG para um token de participante.
func TestAdminQRImage(t *testing.T) {
	srv, store := newAdminTestServer(t)
	p, _ := store.CreateParticipant("Helena", "", "")
	client := adminClient(t)
	loginAdmin(t, srv, client, "senha")

	resp, err := client.Get(srv.URL + "/admin/qr/" + p.Token)
	if err != nil {
		t.Fatalf("qr image: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("qr esperava 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("qr Content-Type = %q, want image/png", ct)
	}
	// Assinatura PNG.
	if len(body) < 8 || string(body[1:4]) != "PNG" {
		t.Fatalf("resposta nao parece um PNG")
	}
}

// Logout admin limpa a sessão e redireciona pro login.
func TestAdminLogout(t *testing.T) {
	srv, _ := newAdminTestServer(t)
	client := adminClient(t)
	loginAdmin(t, srv, client, "senha")

	resp, err := client.Get(srv.URL + "/admin/logout")
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("logout esperava 303, got %d", resp.StatusCode)
	}

	// Apos logout, rota protegida deve redirecionar pro login.
	resp, err = client.Get(srv.URL + "/admin/dashboard")
	if err != nil {
		t.Fatalf("dashboard pos-logout: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("pos-logout dashboard deveria redirecionar (303), got %d", resp.StatusCode)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// rankLANIPs deve descartar IP público/link-local e colocar o IP de cliente
// real (192.168.0.109 no evento) à frente dos adaptadores virtuais (.1).
func TestRankLANIPs(t *testing.T) {
	in := []string{
		"54.232.189.113", // público — deve ser descartado
		"192.168.80.1",   // adaptador virtual (Hyper-V/VirtualBox)
		"172.20.224.1",   // adaptador WSL
		"192.168.0.109",  // IP real do notebook na LAN do evento
		"169.254.10.5",   // link-local — deve ser descartado
	}
	got := rankLANIPs(in)
	want := []string{"192.168.0.109", "192.168.80.1", "172.20.224.1"}
	if len(got) != len(want) {
		t.Fatalf("rankLANIPs(%v) = %v; queria %v", in, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rankLANIPs ordem errada: got %v, want %v", got, want)
		}
	}
}

func TestAIFailureReason(t *testing.T) {
	cases := []struct {
		name string
		err  string
		want string // substring esperada na mensagem
	}{
		{"chave invalida 401", "gemini status 401: invalid api key", "Chave da IA invalida"},
		{"sem permissao 403", "gemini status 403: forbidden", "Chave da IA invalida"},
		{"creditos esgotados", `gemini status 429: {"error":{"message":"Your prepayment credits are depleted","status":"RESOURCE_EXHAUSTED"}}`, "Creditos da IA esgotados"},
		{"rate limit 429", "gemini status 429: quota exceeded for requests per minute", "Limite de uso da IA"},
		{"timeout deadline", "context deadline exceeded", "timeout"},
		{"timeout client", "Post ...: net/http: request canceled (Client.Timeout exceeded)", "timeout"},
		{"sem imagem", "gemini nao retornou imagem", "nao conseguiu gerar"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := aiFailureReason(errors.New(c.err))
			if !strings.Contains(got, c.want) {
				t.Fatalf("aiFailureReason(%q) = %q; queria conter %q", c.err, got, c.want)
			}
		})
	}
}
