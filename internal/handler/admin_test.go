package handler

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
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
	if !strings.Contains(string(body), "/s/"+p.Token) {
		t.Fatalf("folha de QR deveria conter a URL do participante (/s/%s)", p.Token)
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

// Página de cartões renderiza com a URL (/s/token) de cada participante.
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
	if !strings.Contains(string(body), "/s/"+p.Token) {
		t.Fatalf("cartões deveriam conter a URL do participante (/s/%s)", p.Token)
	}
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
