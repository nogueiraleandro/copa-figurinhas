package handler

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sync"
	"testing"

	"copa/internal/model"
)

// Simula ~40 celulares se registrando e coletando todos ao mesmo tempo,
// como no pico do evento. Valida ausencia de erros, deadlocks e contagem correta
// sob concorrencia (SQLite serializa escritas via pool de 1 conexao).
func TestLoadConcurrentPhones(t *testing.T) {
	if testing.Short() {
		t.Skip("pulando teste de carga em -short")
	}
	const n = 40
	srv, store := newTestServer(t)

	people := make([]*model.Participant, n)
	for i := 0; i < n; i++ {
		p, err := store.CreateParticipant(fmt.Sprintf("Pessoa %02d", i), "", "")
		if err != nil {
			t.Fatalf("create participant: %v", err)
		}
		people[i] = p
	}

	newPhone := func() *http.Client {
		jar, _ := cookiejar.New(nil)
		return &http.Client{
			Jar: jar,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}

	var wg sync.WaitGroup
	errCh := make(chan error, n*4)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			client := newPhone()
			me := people[idx]

			// Registrar (1o QR = eu).
			resp, err := client.PostForm(srv.URL+"/s/"+me.Token+"/confirm", url.Values{"choice": {"yes"}})
			if err != nil {
				errCh <- fmt.Errorf("phone %d register: %w", idx, err)
				return
			}
			resp.Body.Close()

			// Coletar todo mundo (inclusive ordem variada por causa do scheduling).
			for j := 0; j < n; j++ {
				resp, err := client.Get(srv.URL + "/s/" + people[j].Token)
				if err != nil {
					errCh <- fmt.Errorf("phone %d collect %d: %w", idx, j, err)
					return
				}
				resp.Body.Close()
			}
		}(i)
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Error(err)
	}

	// Cada celular deve ter o album completo (n figurinhas).
	for i := 0; i < n; i++ {
		cnt, err := store.CountCollection(people[i].ID)
		if err != nil {
			t.Fatalf("count %d: %v", i, err)
		}
		if cnt != n {
			t.Errorf("pessoa %d tem %d figurinhas, esperado %d", i, cnt, n)
		}
		if complete, _ := store.IsComplete(people[i].ID); !complete {
			t.Errorf("pessoa %d deveria ter album completo", i)
		}
	}

	// Ranking deve listar os n participantes, todos completos.
	rank, err := store.GetRanking()
	if err != nil {
		t.Fatalf("ranking: %v", err)
	}
	if len(rank) != n {
		t.Fatalf("ranking deveria ter %d, got %d", n, len(rank))
	}
	for _, e := range rank {
		if !e.Complete {
			t.Errorf("participante %d deveria estar completo no ranking", e.ParticipantID)
		}
	}
}
