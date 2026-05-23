package handler

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Com o apito no passado, /tv exibe a tela de campeao com o vencedor.
func TestTVShowsChampionAfterKickoff(t *testing.T) {
	srv, store := newTestServer(t)
	a, _ := store.CreateParticipant("Alice", "", "")
	b, _ := store.CreateParticipant("Bob", "", "")
	store.CreateDevice(a.ID) //nolint:errcheck
	store.CreateDevice(b.ID) //nolint:errcheck
	// Alice completa o album (2 ativos).
	store.AddToCollection(a.ID, a.ID) //nolint:errcheck
	store.AddToCollection(a.ID, b.ID) //nolint:errcheck

	// Define o apito no passado.
	set, _ := store.GetSetting()
	past := time.Now().Add(-1 * time.Minute)
	set.KickoffAt = &past
	if err := store.SaveSetting(set); err != nil {
		t.Fatalf("save setting: %v", err)
	}

	resp, err := http.Get(srv.URL + "/tv")
	if err != nil {
		t.Fatalf("get tv: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	html := string(body)
	if !strings.Contains(html, "GRANDE CAMPEÃO") {
		t.Fatalf("apos o apito, /tv deveria mostrar a tela de campeao")
	}
	if !strings.Contains(html, "Alice") {
		t.Fatalf("o campeao deveria ser Alice")
	}
}

// Antes do apito, /tv mostra o ranking ao vivo (sem tela de campeao).
func TestTVLiveBeforeKickoff(t *testing.T) {
	srv, store := newTestServer(t)
	a, _ := store.CreateParticipant("Alice", "", "")
	store.CreateDevice(a.ID)           //nolint:errcheck
	store.AddToCollection(a.ID, a.ID)  //nolint:errcheck

	set, _ := store.GetSetting()
	future := time.Now().Add(1 * time.Hour)
	set.KickoffAt = &future
	store.SaveSetting(set) //nolint:errcheck

	resp, err := http.Get(srv.URL + "/tv")
	if err != nil {
		t.Fatalf("get tv: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), "GRANDE CAMPEÃO") {
		t.Fatalf("antes do apito nao deveria mostrar tela de campeao")
	}
}
