package service

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	idb "copa/internal/db"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	database, err := idb.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return NewStore(database)
}

func mustParticipant(t *testing.T, s *Store, name string) int64 {
	t.Helper()
	p, err := s.CreateParticipant(name, "", "")
	if err != nil {
		t.Fatalf("create participant %s: %v", name, err)
	}
	return p.ID
}

// Coletar a mesma figurinha duas vezes nao deve duplicar nem dar erro.
func TestAddToCollectionIdempotent(t *testing.T) {
	s := newTestStore(t)
	owner := mustParticipant(t, s, "Owner")
	sticker := mustParticipant(t, s, "Sticker")

	isNew, err := s.AddToCollection(owner, sticker)
	if err != nil || !isNew {
		t.Fatalf("primeira coleta: isNew=%v err=%v (esperado isNew=true)", isNew, err)
	}
	isNew, err = s.AddToCollection(owner, sticker)
	if err != nil || isNew {
		t.Fatalf("segunda coleta: isNew=%v err=%v (esperado isNew=false)", isNew, err)
	}
	n, _ := s.CountCollection(owner)
	if n != 1 {
		t.Fatalf("CountCollection=%d, esperado 1", n)
	}
}

// CreateDevice deve vincular o device ao participante (identidade no celular).
func TestCreateDeviceLinksParticipant(t *testing.T) {
	s := newTestStore(t)
	pid := mustParticipant(t, s, "Ana")

	dev, err := s.CreateDevice(pid)
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	got, err := s.GetDeviceByCookie(dev.CookieToken)
	if err != nil {
		t.Fatalf("get device by cookie: %v", err)
	}
	if got.ParticipantID != pid {
		t.Fatalf("device aponta pra %d, esperado %d", got.ParticipantID, pid)
	}
	p, _ := s.GetParticipantByID(pid)
	if p.ClaimedDeviceID == nil || *p.ClaimedDeviceID != dev.ID {
		t.Fatalf("participante nao foi vinculado ao device")
	}
}

// Recuperacao: re-escanear a propria figurinha num celular novo re-vincula o album.
func TestReassignDeviceRecovery(t *testing.T) {
	s := newTestStore(t)
	pid := mustParticipant(t, s, "Bruno")

	// Celular novo (sem identidade) cria um device.
	newDev, err := s.CreateDevice(pid)
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	// Simula um device antigo que tambem apontava pra esse participante.
	oldDev, _ := s.CreateDevice(pid)

	if err := s.ReassignDevice(newDev.ID, pid); err != nil {
		t.Fatalf("reassign: %v", err)
	}
	p, _ := s.GetParticipantByID(pid)
	if p.ClaimedDeviceID == nil || *p.ClaimedDeviceID != newDev.ID {
		t.Fatalf("apos recuperacao o participante deveria apontar pro device novo (%d), got %v", newDev.ID, p.ClaimedDeviceID)
	}
	// O device antigo nao deve mais ser o "claimed" do participante.
	if p.ClaimedDeviceID != nil && *p.ClaimedDeviceID == oldDev.ID {
		t.Fatalf("device antigo ainda vinculado")
	}
}

// Album completo = coletar todos os participantes ativos. Desativar um muda o alvo.
func TestIsCompleteRespectsActiveRoster(t *testing.T) {
	s := newTestStore(t)
	owner := mustParticipant(t, s, "Owner")
	a := mustParticipant(t, s, "A")
	b := mustParticipant(t, s, "B")

	// 3 ativos no total (owner, a, b). Coleta owner + a => 2 de 3, incompleto.
	s.AddToCollection(owner, owner)
	s.AddToCollection(owner, a)
	if complete, _ := s.IsComplete(owner); complete {
		t.Fatalf("nao deveria estar completo com 2 de 3")
	}
	// Coleta b => 3 de 3, completo.
	s.AddToCollection(owner, b)
	if complete, _ := s.IsComplete(owner); !complete {
		t.Fatalf("deveria estar completo com 3 de 3")
	}
	// Chega um participante novo (elenco cresce) => volta a ficar incompleto.
	c := mustParticipant(t, s, "C")
	if complete, _ := s.IsComplete(owner); complete {
		t.Fatalf("com novo participante deveria voltar a incompleto")
	}
	// Desativa o novo => completo de novo (travar elenco / remover).
	if err := s.SetParticipantActive(c, false); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if complete, _ := s.IsComplete(owner); !complete {
		t.Fatalf("apos desativar o novo, deveria estar completo")
	}
}

func TestCompletedAtSet(t *testing.T) {
	s := newTestStore(t)
	owner := mustParticipant(t, s, "Owner")
	other := mustParticipant(t, s, "Other")
	s.AddToCollection(owner, owner)
	if at, _ := s.CompletedAt(owner); at != nil {
		t.Fatalf("nao deveria ter CompletedAt (incompleto)")
	}
	s.AddToCollection(owner, other)
	at, err := s.CompletedAt(owner)
	if err != nil {
		t.Fatalf("CompletedAt err: %v", err)
	}
	if at == nil {
		t.Fatalf("deveria ter CompletedAt apos completar")
	}
}

// Ranking: ordena por quantidade desc; empate desempata por quem chegou ao maximo antes.
func TestRankingOrderAndTiebreak(t *testing.T) {
	s := newTestStore(t)
	p1 := mustParticipant(t, s, "P1")
	p2 := mustParticipant(t, s, "P2")
	p3 := mustParticipant(t, s, "P3")

	// Todos precisam de identidade (device) pra aparecer no ranking.
	for _, id := range []int64{p1, p2, p3} {
		if _, err := s.CreateDevice(id); err != nil {
			t.Fatalf("device: %v", err)
		}
	}

	// p1 e p2 coletam 2 cada; p3 coleta 1. p1 chega ao max ANTES de p2.
	s.AddToCollection(p1, p1)
	s.AddToCollection(p1, p2)
	time.Sleep(10 * time.Millisecond)
	s.AddToCollection(p2, p1)
	s.AddToCollection(p2, p2)
	s.AddToCollection(p3, p3)

	rank, err := s.GetRanking()
	if err != nil {
		t.Fatalf("ranking: %v", err)
	}
	if len(rank) != 3 {
		t.Fatalf("esperado 3 no ranking, got %d", len(rank))
	}
	if rank[0].ParticipantID != p1 {
		t.Fatalf("1o lugar deveria ser p1 (mesmo placar, chegou antes), got %d", rank[0].ParticipantID)
	}
	if rank[1].ParticipantID != p2 {
		t.Fatalf("2o lugar deveria ser p2, got %d", rank[1].ParticipantID)
	}
	if rank[2].ParticipantID != p3 || rank[2].Count != 1 {
		t.Fatalf("3o lugar deveria ser p3 com 1, got id=%d count=%d", rank[2].ParticipantID, rank[2].Count)
	}
}

// Vencedor: quem completa primeiro vence quem tem mais figurinhas.
func TestWinnerFirstToComplete(t *testing.T) {
	s := newTestStore(t)
	a := mustParticipant(t, s, "A")
	b := mustParticipant(t, s, "B")
	c := mustParticipant(t, s, "C")
	for _, id := range []int64{a, b, c} {
		s.CreateDevice(id)
	}
	// 3 ativos. B completa (3/3). A fica com 2/3. Mesmo assim o vencedor e B.
	s.AddToCollection(b, a)
	s.AddToCollection(b, b)
	s.AddToCollection(b, c)
	s.AddToCollection(a, a)
	s.AddToCollection(a, b)

	win, err := s.Winner(nil)
	if err != nil {
		t.Fatalf("winner: %v", err)
	}
	if win == nil || win.ParticipantID != b {
		t.Fatalf("vencedor deveria ser B (primeiro a completar), got %v", win)
	}
}

// Sem ninguem completo, vence quem tem mais figurinhas.
func TestWinnerMostStickers(t *testing.T) {
	s := newTestStore(t)
	a := mustParticipant(t, s, "A")
	b := mustParticipant(t, s, "B")
	mustParticipant(t, s, "C") // 3 ativos, ninguem completa
	s.CreateDevice(a)
	s.CreateDevice(b)
	s.AddToCollection(a, a)
	s.AddToCollection(a, b) // A: 2
	s.AddToCollection(b, b) // B: 1

	win, _ := s.Winner(nil)
	if win == nil || win.ParticipantID != a {
		t.Fatalf("vencedor deveria ser A (mais figurinhas), got %v", win)
	}
}

// Congelamento: coletas apos o apito nao contam no ranking final.
func TestFinalRankingFreezesAtKickoff(t *testing.T) {
	s := newTestStore(t)
	a := mustParticipant(t, s, "A")
	b := mustParticipant(t, s, "B")
	s.CreateDevice(a)
	s.CreateDevice(b)

	s.AddToCollection(a, a) // antes do apito
	kickoff := time.Now()
	time.Sleep(15 * time.Millisecond)
	s.AddToCollection(a, b) // depois do apito -> nao deve contar no final

	final, err := s.GetFinalRanking(kickoff)
	if err != nil {
		t.Fatalf("final ranking: %v", err)
	}
	for _, e := range final {
		if e.ParticipantID == a {
			if e.Count != 1 {
				t.Fatalf("no apito A tinha 1 figurinha; final marcou %d", e.Count)
			}
		}
	}
	// Ao vivo (sem corte) A tem 2.
	live, _ := s.GetRanking()
	for _, e := range live {
		if e.ParticipantID == a && e.Count != 2 {
			t.Fatalf("ao vivo A deveria ter 2, got %d", e.Count)
		}
	}
}

func TestFinalSnapshotPersistsOfficialResult(t *testing.T) {
	s := newTestStore(t)
	a := mustParticipant(t, s, "A")
	b := mustParticipant(t, s, "B")
	s.CreateDevice(a) //nolint:errcheck
	s.CreateDevice(b) //nolint:errcheck

	s.AddToCollection(a, a) //nolint:errcheck
	kickoff := time.Now()
	time.Sleep(15 * time.Millisecond)
	s.AddToCollection(a, b) // depois do apito: nao entra no snapshot

	snap, err := s.EnsureFinalSnapshot(kickoff)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(snap) == 0 || snap[0].ParticipantID != a || snap[0].Count != 1 || snap[0].Total != 2 {
		t.Fatalf("snapshot deveria guardar A com 1/2, got %#v", snap)
	}

	if err := s.SetParticipantActive(b, false); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	stored, frozenAt, err := s.GetStoredFinalRanking()
	if err != nil {
		t.Fatalf("stored snapshot: %v", err)
	}
	if frozenAt == nil || idb.TimeToString(*frozenAt) != idb.TimeToString(kickoff) {
		t.Fatalf("frozen_at deveria ser o apito original")
	}
	if len(stored) == 0 || stored[0].Count != 1 || stored[0].Total != 2 {
		t.Fatalf("snapshot oficial nao deveria mudar apos editar elenco, got %#v", stored)
	}
}

// Quem nao reivindicou identidade (sem device) nao aparece no ranking.
func TestRankingExcludesUnclaimed(t *testing.T) {
	s := newTestStore(t)
	claimed := mustParticipant(t, s, "Claimed")
	mustParticipant(t, s, "Unclaimed")
	s.CreateDevice(claimed)
	s.AddToCollection(claimed, claimed)

	rank, err := s.GetRanking()
	if err != nil {
		t.Fatalf("ranking: %v", err)
	}
	if len(rank) != 1 || rank[0].ParticipantID != claimed {
		t.Fatalf("ranking deveria conter so o participante reivindicado, got %d entradas", len(rank))
	}
}

// ResetGameData apaga participantes/devices/coleções mas preserva base_url e senha.
func TestResetGameData(t *testing.T) {
	s := newTestStore(t)

	// Config que deve sobreviver ao reset.
	set, _ := s.GetSetting()
	set.BaseURL = "http://192.168.0.50:8080"
	set.RosterLocked = true
	kickoff := time.Now()
	set.KickoffAt = &kickoff
	if err := s.SaveSetting(set); err != nil {
		t.Fatalf("save setting: %v", err)
	}
	if err := s.SetAdminPasswordHash("hash-secreto"); err != nil {
		t.Fatalf("set hash: %v", err)
	}

	// Dados do jogo.
	a := mustParticipant(t, s, "Ana")
	b := mustParticipant(t, s, "Bruno")
	s.CreateDevice(a)       //nolint:errcheck
	s.AddToCollection(a, a) //nolint:errcheck
	s.AddToCollection(a, b) //nolint:errcheck

	if err := s.ResetGameData(); err != nil {
		t.Fatalf("reset: %v", err)
	}

	people, _ := s.ListParticipants()
	if len(people) != 0 {
		t.Fatalf("após reset deveria não haver participantes, got %d", len(people))
	}
	got, err := s.GetSetting()
	if err != nil {
		t.Fatalf("get setting pós-reset: %v", err)
	}
	if got.BaseURL != "http://192.168.0.50:8080" {
		t.Errorf("base_url deveria ser preservado, got %q", got.BaseURL)
	}
	if got.AdminPasswordHash != "hash-secreto" {
		t.Errorf("senha de admin deveria ser preservada, got %q", got.AdminPasswordHash)
	}
	if got.KickoffAt != nil {
		t.Errorf("kickoff_at deveria ser zerado")
	}
	if got.RosterLocked {
		t.Errorf("roster_locked deveria voltar a false")
	}
}

// RestoreFrom repõe os dados de um backup mantendo o servidor (store) vivo.
func TestRestoreFrom(t *testing.T) {
	// Store A com dados.
	a := newTestStore(t)
	ana := mustParticipant(t, a, "Ana")
	bruno := mustParticipant(t, a, "Bruno")
	a.CreateDevice(ana)           //nolint:errcheck
	a.AddToCollection(ana, ana)   //nolint:errcheck
	a.AddToCollection(ana, bruno) //nolint:errcheck

	backupPath := filepath.Join(t.TempDir(), "bk.db")
	if err := a.BackupTo(backupPath); err != nil {
		t.Fatalf("backup: %v", err)
	}

	// Store B vazio -> restaura do backup.
	b := newTestStore(t)
	if n, _ := b.CountActiveParticipants(); n != 0 {
		t.Fatalf("store B deveria começar vazio, got %d", n)
	}
	if err := b.RestoreFrom(backupPath); err != nil {
		t.Fatalf("restore: %v", err)
	}

	people, _ := b.ListParticipants()
	if len(people) != 2 {
		t.Fatalf("após restore deveria haver 2 participantes, got %d", len(people))
	}
	n, _ := b.CountCollection(ana)
	if n != 2 {
		t.Fatalf("coleção da Ana deveria ter 2 figurinhas após restore, got %d", n)
	}
	// Identidade/token preservados: dá pra achar a Ana pelo token original.
	if _, err := b.GetParticipantByToken(people[0].Token); err != nil {
		t.Fatalf("token deveria ser restaurado: %v", err)
	}
}

func TestRestoreFromOldBackupWithoutAIColumns(t *testing.T) {
	oldPath := filepath.Join(t.TempDir(), "old.db")
	oldDB, err := sql.Open("sqlite", oldPath)
	if err != nil {
		t.Fatalf("open old db: %v", err)
	}
	_, err = oldDB.Exec(`
		CREATE TABLE setting (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			base_url TEXT NOT NULL DEFAULT 'http://localhost:8080',
			kickoff_at TEXT,
			roster_locked INTEGER NOT NULL DEFAULT 0,
			admin_password_hash TEXT NOT NULL DEFAULT ''
		);
		INSERT INTO setting (id, base_url, kickoff_at, roster_locked, admin_password_hash)
		VALUES (1, 'http://192.168.0.50:8080', NULL, 0, 'hash-antigo');
	`)
	oldDB.Close()
	if err != nil {
		t.Fatalf("seed old db: %v", err)
	}

	s := newTestStore(t)
	if err := s.RestoreFrom(oldPath); err != nil {
		t.Fatalf("restore old backup: %v", err)
	}
	set, err := s.GetSetting()
	if err != nil {
		t.Fatalf("get setting: %v", err)
	}
	if set.BaseURL != "http://192.168.0.50:8080" || set.AdminPasswordHash != "hash-antigo" {
		t.Fatalf("campos antigos nao restauraram: %#v", set)
	}
	if set.AIModel != "gemini-2.5-flash-image" || set.GeminiAPIKey != "" || set.AIPrompt != "" || set.AIReferencePath != "" {
		t.Fatalf("campos novos deveriam ficar com defaults: %#v", set)
	}
}
