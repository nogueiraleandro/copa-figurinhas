package handler

import (
	"encoding/json"
	"time"

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
}

func NewNotifier(store *service.Store, hub *sse.Hub) *Notifier {
	n := &Notifier{
		store:       store,
		hub:         hub,
		trigger:     make(chan struct{}, 1),
		minInterval: 250 * time.Millisecond,
	}
	go n.loop()
	return n
}

// Ranking solicita um broadcast do ranking (nao bloqueante, coalescido).
func (n *Notifier) Ranking() {
	select {
	case n.trigger <- struct{}{}:
	default: // ja existe um pendente; o estado mais recente sera lido na hora do envio
	}
}

func (n *Notifier) loop() {
	for range n.trigger {
		n.sendRanking()
		time.Sleep(n.minInterval) // rate limit: rajadas durante este intervalo colapsam em 1 envio
	}
}

// rankingJSON retorna o JSON do ranking atual (vazio em caso de erro).
func (n *Notifier) rankingJSON() string {
	ranking, err := n.store.GetRanking()
	if err != nil {
		return ""
	}
	data, err := json.Marshal(ranking)
	if err != nil {
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
	data, _ := json.Marshal(map[string]interface{}{
		"name":        owner.Name,
		"nickname":    owner.Nickname,
		"completedAt": completedAt,
	})
	n.hub.Broadcast("complete", string(data))
}
