package main

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"copa/internal/db"
	"copa/internal/service"
)

// cleanOldBackups mantem apenas os 5 backups mais recentes (por ordem de nome,
// que e cronologica pelo timestamp no nome do arquivo).
func TestCleanOldBackupsKeepsLastFive(t *testing.T) {
	dir := t.TempDir()

	// Cria 8 backups com timestamps crescentes no nome.
	var created []string
	for i := 1; i <= 8; i++ {
		name := fmt.Sprintf("copa-backup-202606%02d-120000.db", i)
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		created = append(created, name)
	}
	// Um arquivo nao-backup nao deve ser tocado.
	other := filepath.Join(dir, "copa.db")
	if err := os.WriteFile(other, []byte("db"), 0644); err != nil {
		t.Fatalf("write other: %v", err)
	}

	cleanOldBackups(dir)

	entries, _ := os.ReadDir(dir)
	var backups []string
	var keptOther bool
	for _, e := range entries {
		n := e.Name()
		if n == "copa.db" {
			keptOther = true
			continue
		}
		backups = append(backups, n)
	}
	sort.Strings(backups)

	if len(backups) != 5 {
		t.Fatalf("deveria manter 5 backups, got %d: %v", len(backups), backups)
	}
	// Deve manter os 5 mais recentes (dias 04..08).
	want := created[3:] // copa-backup-...04 ... 08
	sort.Strings(want)
	for i := range want {
		if backups[i] != want[i] {
			t.Fatalf("backups mantidos errados: got %v, want %v", backups, want)
		}
	}
	if !keptOther {
		t.Fatalf("arquivo nao-backup (copa.db) nao deveria ser removido")
	}
}

// backupFull gera um backup COMPLETO (banco + fotos) como copa-backup-*.zip.
func TestBackupFullCreatesZip(t *testing.T) {
	dir := t.TempDir()
	uploads := t.TempDir()
	// Uma foto qualquer em uploads/ para garantir que ela entra no zip.
	if err := os.WriteFile(filepath.Join(uploads, "foto1.jpg"), []byte("jpeg"), 0644); err != nil {
		t.Fatalf("write foto: %v", err)
	}
	database, err := db.Open(dir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()
	store := service.NewStore(database)

	backupFull(store, dir, uploads)

	entries, _ := os.ReadDir(dir)
	var zipPath string
	for _, e := range entries {
		if matchesBackupName(e.Name()) && strings.HasSuffix(e.Name(), ".zip") {
			zipPath = filepath.Join(dir, e.Name())
		}
	}
	if zipPath == "" {
		t.Fatalf("backupFull deveria criar um copa-backup-*.zip em %s", dir)
	}

	// O zip deve conter o banco e a foto.
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("abrir zip: %v", err)
	}
	defer zr.Close()
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if !names["data/copa.db"] {
		t.Errorf("zip deveria conter data/copa.db; tem %v", names)
	}
	if !names["uploads/foto1.jpg"] {
		t.Errorf("zip deveria conter as fotos de uploads/; tem %v", names)
	}
}

// Arquivos temporarios de download/restore (copa-download-*, copa-restore-*) que
// sobraram apos um crash NAO devem contar como backup nem causar a exclusao dos
// backups reais.
func TestCleanOldBackupsIgnoresTempFiles(t *testing.T) {
	dir := t.TempDir()

	var realBackups []string
	for i := 1; i <= 5; i++ {
		name := fmt.Sprintf("copa-backup-202606%02d-120000.db", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		realBackups = append(realBackups, name)
	}
	// Temporarios com nomes UnixNano (numeros grandes) que ficariam "no fim" numa
	// ordenacao por nome — antes empurrariam os backups reais para fora dos 5.
	temps := []string{
		"copa-download-1717000000000000000.db",
		"copa-restore-1717000000000000001.db",
		"copa-full-db-1717000000000000002.db",
	}
	for _, name := range temps {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	cleanOldBackups(dir)

	entries, _ := os.ReadDir(dir)
	present := map[string]bool{}
	for _, e := range entries {
		present[e.Name()] = true
	}
	for _, name := range realBackups {
		if !present[name] {
			t.Fatalf("backup real %s nao deveria ter sido removido", name)
		}
	}
	// Os temporarios sao ignorados por cleanOldBackups (continuam intactos).
	for _, name := range temps {
		if !present[name] {
			t.Fatalf("arquivo temporario %s nao deveria ser tocado por cleanOldBackups", name)
		}
	}
}
