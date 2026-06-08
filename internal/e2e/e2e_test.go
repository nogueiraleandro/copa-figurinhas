//go:build e2e

// Package e2e roda testes de navegador (Chrome headless via chromedp) contra o
// servidor real montado por internal/app.NewRouter — exercita o JavaScript do
// cliente, o mascote/lottie e as atualizacoes ao vivo via SSE no telao.
//
// Gated por `//go:build e2e`: o `go test ./...` normal nao compila nem roda isto.
// Rode com:  go test ./internal/e2e -tags e2e -v
package e2e

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"copa/internal/app"
	idb "copa/internal/db"
	"copa/internal/handler"
	"copa/internal/service"
	"copa/internal/sse"
)

// findChrome localiza o binario do Chrome/Chromium nas localizacoes usuais.
// Retorna "" se nao encontrar (o teste e entao pulado).
func findChrome() string {
	candidates := []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome"}
	for _, c := range candidates {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	if runtime.GOOS == "windows" {
		for _, p := range []string{
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
			os.Getenv("LOCALAPPDATA") + `\Google\Chrome\Application\chrome.exe`,
		} {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	for _, p := range []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/usr/bin/google-chrome",
		"/usr/bin/chromium",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

type testApp struct {
	srv   *httptest.Server
	store *service.Store
}

// startApp sobe o servidor completo (mesmas rotas da producao) sobre um banco temporario.
func startApp(t *testing.T) *testApp {
	t.Helper()
	database, err := idb.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	store := service.NewStore(database)
	hub := sse.NewHub()
	notifier := handler.NewNotifier(store, hub)
	tmpl, err := handler.NewTemplates(os.DirFS("../../cmd/copa/web"))
	if err != nil {
		t.Fatalf("templates: %v", err)
	}
	staticFS := os.DirFS("../../cmd/copa/web/static")

	mux := app.NewRouter(app.Deps{
		Store:      store,
		Hub:        hub,
		Notifier:   notifier,
		Tmpl:       tmpl,
		StaticFS:   staticFS,
		UploadsDir: t.TempDir(),
		DataDir:    t.TempDir(),
		ListenAddr: ":8080",
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &testApp{srv: srv, store: store}
}

// newBrowser cria um contexto chromedp headless; pula o teste se nao houver Chrome.
func newBrowser(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	chrome := findChrome()
	if chrome == "" {
		t.Skip("Chrome nao encontrado — pulando teste de navegador (E2E)")
	}
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chrome),
		chromedp.Headless,
		chromedp.DisableGPU,
		chromedp.NoSandbox,
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, cancelCtx := chromedp.NewContext(allocCtx)
	cancel := func() { cancelCtx(); cancelAlloc() }

	// Timeout global do teste de navegador.
	ctx, cancelTimeout := context.WithTimeout(ctx, 30*time.Second)
	t.Cleanup(func() { cancelTimeout(); cancel() })

	// Garante que o browser realmente inicia; se nao, pula com mensagem clara.
	if err := chromedp.Run(ctx); err != nil {
		t.Skipf("nao foi possivel iniciar o Chrome (%v) — pulando E2E", err)
	}
	return ctx, cancel
}

// setSessionCookie injeta o cookie de sessao do convidado no browser.
func setSessionCookie(srvURL, token string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		return network.SetCookie("copa_session", token).
			WithURL(srvURL).
			Do(ctx)
	})
}

// O album de um convidado logado carrega e o mascote (div.mascote) e injetado pelo JS.
func TestAlbumLoadsMascot(t *testing.T) {
	a := startApp(t)
	ctx, _ := newBrowser(t)

	ana, _ := a.store.CreateParticipant("Ana", "", "")
	dev, err := a.store.CreateDevice(ana.ID)
	if err != nil {
		t.Fatalf("device: %v", err)
	}
	a.store.AddToCollection(ana.ID, ana.ID) //nolint:errcheck

	var mascotCount int
	err = chromedp.Run(ctx,
		network.Enable(),
		setSessionCookie(a.srv.URL, dev.CookieToken),
		chromedp.Navigate(a.srv.URL+"/album"),
		chromedp.WaitVisible(`.sticker-grid`, chromedp.ByQuery),
		// mascote.js cria <div class="mascote"> no body ao carregar.
		chromedp.WaitReady(`.mascote`, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelectorAll('.mascote').length`, &mascotCount),
	)
	if err != nil {
		t.Fatalf("navegacao /album: %v", err)
	}
	if mascotCount < 1 {
		t.Fatalf("o mascote (.mascote) deveria ser injetado pelo mascote.js")
	}
}

// /reveal mostra o nome e a inicial maiuscula (renderizada pelo template).
func TestRevealShowsName(t *testing.T) {
	a := startApp(t)
	ctx, _ := newBrowser(t)

	var body string
	err := chromedp.Run(ctx,
		chromedp.Navigate(a.srv.URL+"/reveal?name="+url.QueryEscape("José Silva")+"&complete=0"),
		chromedp.WaitReady(`body`, chromedp.ByQuery),
		chromedp.Text(`body`, &body, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navegacao /reveal: %v", err)
	}
	if !strings.Contains(body, "José") {
		t.Fatalf("a tela de reveal deveria conter o nome 'José'; body: %.200q", body)
	}
}

// Teste-chave: o telao conecta no /sse e atualiza o ranking AO VIVO (sem reload)
// quando alguem se registra via HTTP em paralelo.
func TestTVLiveRankingViaSSE(t *testing.T) {
	a := startApp(t)
	ctx, _ := newBrowser(t)

	ana, _ := a.store.CreateParticipant("Ana Telao", "", "")

	// Abre o telao e espera a conexao SSE ficar "ao vivo".
	var connText string
	if err := chromedp.Run(ctx,
		chromedp.Navigate(a.srv.URL+"/tv"),
		chromedp.WaitVisible(`#rankingList`, chromedp.ByQuery),
		// #connDot vira "🟢 ao vivo" no evtSource.onopen.
		waitTextContains(`#connDot`, "ao vivo", 10*time.Second),
		chromedp.Text(`#connDot`, &connText, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("abrir telao / conectar SSE: %v", err)
	}
	if !strings.Contains(connText, "ao vivo") {
		t.Fatalf("telao deveria indicar conexao SSE ao vivo, got %q", connText)
	}

	// Em paralelo, Ana se registra via HTTP (dispara broadcast de ranking).
	registerViaHTTP(t, a.srv.URL, ana.Token)

	// O ranking do telao deve atualizar sozinho (sem reload) com o nome da Ana.
	if err := chromedp.Run(ctx,
		waitTextContains(`#rankingList`, "Ana Telao", 10*time.Second),
	); err != nil {
		var rankingText string
		_ = chromedp.Run(ctx, chromedp.Text(`#rankingList`, &rankingText, chromedp.ByQuery))
		t.Fatalf("o ranking do telao deveria atualizar ao vivo via SSE; conteudo atual: %.300q (err: %v)", rankingText, err)
	}
}

// prefers-reduced-motion: mascotPlay nao deve disparar animacao (retorna cedo).
func TestReducedMotionSkipsAnimation(t *testing.T) {
	a := startApp(t)
	ctx, _ := newBrowser(t)

	ana, _ := a.store.CreateParticipant("Ana", "", "")
	dev, _ := a.store.CreateDevice(ana.ID)
	a.store.AddToCollection(ana.ID, ana.ID) //nolint:errcheck

	var reduced bool
	err := chromedp.Run(ctx,
		network.Enable(),
		emulateReducedMotion(),
		setSessionCookie(a.srv.URL, dev.CookieToken),
		chromedp.Navigate(a.srv.URL+"/album"),
		chromedp.WaitReady(`.mascote`, chromedp.ByQuery),
		chromedp.Evaluate(`window.matchMedia('(prefers-reduced-motion: reduce)').matches`, &reduced),
	)
	if err != nil {
		t.Fatalf("navegacao /album (reduced motion): %v", err)
	}
	if !reduced {
		t.Fatalf("o navegador deveria reportar prefers-reduced-motion: reduce")
	}
}

// ---- helpers ----

// waitTextContains espera ate o texto do seletor conter substr (ou estoura timeout).
func waitTextContains(sel, substr string, timeout time.Duration) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		deadline := time.Now().Add(timeout)
		for {
			var txt string
			if err := chromedp.Text(sel, &txt, chromedp.ByQuery, chromedp.AtLeast(0)).Do(ctx); err == nil {
				if strings.Contains(txt, substr) {
					return nil
				}
			}
			if time.Now().After(deadline) {
				return context.DeadlineExceeded
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(200 * time.Millisecond):
			}
		}
	})
}

// emulateReducedMotion ativa a media feature prefers-reduced-motion: reduce no Chrome.
func emulateReducedMotion() chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		return emulation.SetEmulatedMedia().
			WithFeatures([]*emulation.MediaFeature{
				{Name: "prefers-reduced-motion", Value: "reduce"},
			}).
			Do(ctx)
	})
}

// registerViaHTTP simula um celular escaneando a propria figurinha e confirmando
// "sou eu", o que cria o device e dispara o broadcast de ranking.
func registerViaHTTP(t *testing.T, srvURL, token string) {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	resp, err := client.PostForm(srvURL+"/s/"+token+"/confirm", url.Values{"choice": {"yes"}})
	if err != nil {
		t.Fatalf("registrar via http: %v", err)
	}
	resp.Body.Close()
}
