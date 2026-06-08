package db

import (
	"testing"
	"time"
)

// Open cria o banco, roda as migrations e deixa todas as tabelas prontas.
func TestOpenCreatesSchema(t *testing.T) {
	dir := t.TempDir()
	database, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	tables := []string{"participant", "device", "collection", "setting", "final_snapshot", "final_ranking"}
	for _, tbl := range tables {
		var n int
		if err := database.QueryRow(`SELECT COUNT(*) FROM ` + tbl).Scan(&n); err != nil {
			t.Fatalf("tabela %q deveria existir apos migrate: %v", tbl, err)
		}
	}

	// A linha unica de setting deve ser semeada.
	var baseURL string
	if err := database.QueryRow(`SELECT base_url FROM setting WHERE id=1`).Scan(&baseURL); err != nil {
		t.Fatalf("setting id=1 deveria existir: %v", err)
	}
	if baseURL == "" {
		t.Fatalf("base_url default nao deveria ser vazio")
	}

	// As colunas adicionadas por ALTER devem existir.
	for _, col := range []string{"gemini_api_key", "ai_model", "ai_prompt", "ai_reference_path"} {
		if err := database.QueryRow(`SELECT ` + col + ` FROM setting WHERE id=1`).Scan(new(string)); err != nil {
			t.Fatalf("coluna %q deveria existir: %v", col, err)
		}
	}
	for _, col := range []string{"team", "info_date", "height", "weight", "phrase"} {
		var n int
		if err := database.QueryRow(`SELECT COUNT(` + col + `) FROM participant`).Scan(&n); err != nil {
			t.Fatalf("coluna participant.%q deveria existir: %v", col, err)
		}
	}
}

// Reabrir o mesmo diretorio e idempotente: migrate roda de novo sem erro nem
// duplicar a linha de setting.
func TestOpenIdempotent(t *testing.T) {
	dir := t.TempDir()

	db1, err := Open(dir)
	if err != nil {
		t.Fatalf("open 1: %v", err)
	}
	// Insere um participante para garantir que reabrir nao apaga dados.
	if _, err := db1.Exec(`INSERT INTO participant (token, name, created_at) VALUES ('tok', 'Ana', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	db1.Close()

	db2, err := Open(dir)
	if err != nil {
		t.Fatalf("open 2 (reabrir): %v", err)
	}
	defer db2.Close()

	var people int
	if err := db2.QueryRow(`SELECT COUNT(*) FROM participant`).Scan(&people); err != nil {
		t.Fatalf("count participant: %v", err)
	}
	if people != 1 {
		t.Fatalf("dados deveriam sobreviver ao reopen; esperava 1 participante, got %d", people)
	}
	var settings int
	if err := db2.QueryRow(`SELECT COUNT(*) FROM setting`).Scan(&settings); err != nil {
		t.Fatalf("count setting: %v", err)
	}
	if settings != 1 {
		t.Fatalf("setting nao deveria duplicar ao reabrir; esperava 1, got %d", settings)
	}
}

func TestTimeRoundTrip(t *testing.T) {
	// Instante com nanos e fuso != UTC: round-trip preserva o instante (em UTC).
	loc := time.FixedZone("BRT", -3*3600)
	original := time.Date(2026, 6, 13, 15, 30, 45, 123456789, loc)

	s := TimeToString(original)
	parsed, err := StringToTime(s)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !parsed.Equal(original) {
		t.Fatalf("round-trip divergente: original=%v parsed=%v (str=%q)", original, parsed, s)
	}
	// A string armazenada deve estar em UTC.
	if parsed.Location() != time.UTC {
		t.Errorf("string armazenada deveria ser UTC, got %v", parsed.Location())
	}
}

func TestStringToTimeInvalid(t *testing.T) {
	if _, err := StringToTime("nao-e-data"); err == nil {
		t.Fatal("string invalida deveria retornar erro")
	}
}
