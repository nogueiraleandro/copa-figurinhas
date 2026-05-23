package handler

import (
	"io"
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

func newTestServer(t *testing.T) (*httptest.Server, *service.Store) {
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

	mux := http.NewServeMux()
	mux.Handle("GET /s/{token}", NewStickerHandler(store, hub, tmpl, notifier))
	mux.Handle("POST /s/{token}/confirm", NewConfirmHandler(store, hub, tmpl, notifier))
	mux.Handle("POST /s/{token}/reclaim", NewReclaimHandler(store, hub, notifier))
	mux.Handle("GET /album", NewAlbumHandler(store, hub, tmpl))
	mux.Handle("GET /reveal", NewRevealHandler(tmpl))
	mux.Handle("POST /logout", NewLogoutHandler())
	mux.Handle("GET /tv", NewTVHandler(store, hub, tmpl))

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, store
}

func newClient(t *testing.T) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // nao seguir redirects, inspecionar Location
		},
	}
}

// Fluxo completo: registrar (1o QR = eu) -> coletar outro -> idempotencia.
func TestQRFlowEndToEnd(t *testing.T) {
	srv, store := newTestServer(t)
	ana, _ := store.CreateParticipant("Ana Beatriz", "", "")
	bruno, _ := store.CreateParticipant("Bruno", "", "")
	client := newClient(t)

	// 1) 1a leitura sem identidade -> tela de confirmacao (200).
	resp, err := client.Get(srv.URL + "/s/" + ana.Token)
	if err != nil {
		t.Fatalf("get sticker: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("confirm esperava 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 2) Confirma "sou eu" -> cria identidade, redireciona pro album.
	resp, err = client.PostForm(srv.URL+"/s/"+ana.Token+"/confirm", url.Values{"choice": {"yes"}})
	if err != nil {
		t.Fatalf("confirm post: %v", err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("confirm yes esperava 303, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/album") {
		t.Fatalf("deveria redirecionar pro album, got %q", loc)
	}
	resp.Body.Close()

	// Apos registro, Ana ja tem a propria figurinha.
	if n, _ := store.CountCollection(ana.ID); n != 1 {
		t.Fatalf("apos registro Ana deveria ter 1 figurinha (a dela), got %d", n)
	}

	// 3) Escaneia o Bruno -> coleta -> redireciona pro /reveal.
	resp, err = client.Get(srv.URL + "/s/" + bruno.Token)
	if err != nil {
		t.Fatalf("get bruno: %v", err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("coleta esperava 303, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/reveal") {
		t.Fatalf("nova figurinha deveria ir pro reveal, got %q", loc)
	}
	resp.Body.Close()

	if has, _ := store.HasSticker(ana.ID, bruno.ID); !has {
		t.Fatalf("Ana deveria ter coletado o Bruno")
	}

	// 4) Escaneia o Bruno de novo -> idempotente -> /album?already=1.
	resp, err = client.Get(srv.URL + "/s/" + bruno.Token)
	if err != nil {
		t.Fatalf("get bruno 2: %v", err)
	}
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "already=1") {
		t.Fatalf("re-coleta deveria ir pro album?already=1, got %q", loc)
	}
	resp.Body.Close()

	if n, _ := store.CountCollection(ana.ID); n != 2 {
		t.Fatalf("Ana deveria ter 2 figurinhas (Ana+Bruno), got %d", n)
	}
}

// 1o QR sendo de OUTRA pessoa: confirmar "nao" nao cria identidade.
func TestConfirmNoDoesNotRegister(t *testing.T) {
	srv, store := newTestServer(t)
	outra, _ := store.CreateParticipant("Outra Pessoa", "", "")
	client := newClient(t)

	resp, err := client.PostForm(srv.URL+"/s/"+outra.Token+"/confirm", url.Values{"choice": {"no"}})
	if err != nil {
		t.Fatalf("confirm no: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("'nao' esperava 200 (tela not_me), got %d", resp.StatusCode)
	}
	resp.Body.Close()

	p, _ := store.GetParticipantByID(outra.ID)
	if p.ClaimedDeviceID != nil {
		t.Fatalf("dizer 'nao' nao deveria registrar/reivindicar a figurinha")
	}
}

// Trocar de jogador: logout limpa a sessao; o album do anterior nao vaza,
// mas a coleta dele continua salva no banco e volta ao re-escanear o QR.
func TestLogoutSwitchesPlayer(t *testing.T) {
	srv, store := newTestServer(t)
	ana, _ := store.CreateParticipant("Ana", "", "")
	bruno, _ := store.CreateParticipant("Bruno", "", "")
	client := newClient(t)

	// Ana entra e coleta o Bruno.
	client.PostForm(srv.URL+"/s/"+ana.Token+"/confirm", url.Values{"choice": {"yes"}}) //nolint:errcheck
	resp, _ := client.Get(srv.URL + "/s/" + bruno.Token)
	resp.Body.Close()
	if n, _ := store.CountCollection(ana.ID); n != 2 {
		t.Fatalf("Ana deveria ter 2 figurinhas antes do logout, got %d", n)
	}

	// Trocar de jogador -> redireciona pro /album e limpa o cookie.
	resp, err := client.PostForm(srv.URL+"/logout", url.Values{})
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("logout esperava 303, got %d", resp.StatusCode)
	}

	// Sem sessao, /album mostra a tela inicial (nao vaza o album da Ana).
	resp, err = client.Get(srv.URL + "/album")
	if err != nil {
		t.Fatalf("album pos-logout: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), "sticker-grid") {
		t.Fatalf("apos logout o album nao deveria ser exibido (vazaria pro proximo jogador)")
	}

	// Os dados da Ana continuam salvos no banco (volta ao re-escanear o QR dela).
	if n, _ := store.CountCollection(ana.ID); n != 2 {
		t.Fatalf("logout nao deveria apagar a colecao da Ana, got %d", n)
	}
}

// QR invalido -> pagina de erro amigavel (404), nao quebra.
func TestInvalidTokenShowsError(t *testing.T) {
	srv, _ := newTestServer(t)
	client := newClient(t)
	resp, err := client.Get(srv.URL + "/s/naoexiste")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("token invalido esperava 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}
