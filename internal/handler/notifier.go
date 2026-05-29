package handler

import (
	"encoding/json"
	"log"
	"time"

	"copa/internal/config"
	"copa/internal/model"
	"copa/internal/service"
	"copa/internal/sse"
)

// Notifier centraliza os broadcasts de ranking com throttle (coalescing).
// Como GetRanking roda um JOIN e o pool SQLite e de 1 conexao, rajadas de scans
// poderiam enfileirar; aqui colapsamos rajadas em no maximo 1 envio por minInterval,
// sempre com borda de saida (o ultimo estado e enviado).
type Notifier struct {
	store       *service.Store
	hub         *sse.Hub
	trigger     chan struct{}
	minInterval time.Duration
	stop        chan struct{}
}

func NewNotifier(store *service.Store, hub *sse.Hub) *Notifier {
	n := &Notifier{
		store:       store,
		hub:         hub,
		trigger:     make(chan struct{}, 1),
		minInterval: config.RankingThrottle,
		stop:        make(chan struct{}),
	}
	go n.loop()
	return n
}

// Close gracefully shuts down the notifier's broadcast loop.
// Must be called before shutdown to prevent goroutine leaks.
func (n *Notifier) Close() error {
	close(n.stop)
	return nil
}

// Ranking solicita um broadcast do ranking (nao bloqueante, coalescido).
func (n *Notifier) Ranking() {
	select {
	case n.trigger <- struct{}{}:
	default: // ja existe um pendente; o estado mais recente sera lido na hora do envio
	}
}

func (n *Notifier) loop() {
	for {
		select {
		case <-n.stop:
			log.Print("notifier: broadcast loop stopped")
			return
		case <-n.trigger:
			n.sendRanking()
			time.Sleep(n.minInterval) // rate limit: rajadas durante este intervalo colapsam em 1 envio
		}
	}
}

// rankingJSON retorna o JSON do ranking atual (vazio em caso de erro).
func (n *Notifier) rankingJSON() string {
	ranking, err := n.store.GetRanking()
	if err != nil {
		log.Printf("notifier: failed to get ranking: %v", err)
		return ""
	}
	data, err := json.Marshal(ranking)
	if err != nil {
		log.Printf("notifier: failed to marshal ranking: %v", err)
		return ""
	}
	return string(data)
}

// RankingSnapshot retorna o frame SSE atual do ranking (para enviar ao conectar).
func (n *Notifier) RankingSnapshot() string {
	j := n.rankingJSON()
	if j == "" {
		return ""
	}
	return sse.FormatMessage("ranking", j)
}

func (n *Notifier) sendRanking() {
	if j := n.rankingJSON(); j != "" {
		n.hub.Broadcast("ranking", j)
	}
}

// Complete dispara o alerta de album completo (evento raro, enviado direto).
func (n *Notifier) Complete(owner *model.Participant, completedAt interface{}) {
	data, err := json.Marshal(map[string]interface{}{
		"name":        owner.Name,
		"nickname":    owner.Nickname,
		"completedAt": completedAt,
	})
	if err != nil {
		log.Printf("notifier: failed to marshal complete event: %v", err)
		return
	}
	n.hub.Broadcast("complete", string(data))
}
