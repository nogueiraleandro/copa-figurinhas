package handler

import (
	"os"
	"testing"
)

// Garante que TODOS os templates parseiam com o funcmap registrado.
// Trava de regressao: funcoes faltando (pct, initial, div100...) quebram o boot do servidor.
func TestAllTemplatesParse(t *testing.T) {
	fsys := os.DirFS("../../cmd/copa/web")
	tmpl, err := NewTemplates(fsys)
	if err != nil {
		t.Fatalf("templates nao parseiam: %v", err)
	}
	for _, name := range []string{
		"album.html", "confirm.html", "reveal.html", "tv.html", "home.html",
		"admin_dashboard.html", "admin_settings.html", "admin_qrsheet.html",
		"admin_system.html", "admin_cards.html",
	} {
		if tmpl.tmpl.Lookup(name) == nil {
			t.Errorf("template %q nao encontrado", name)
		}
	}
}
