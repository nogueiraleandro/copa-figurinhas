package handler

import (
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strings"
)

// Templates holds parsed HTML templates from the embedded FS.
type Templates struct {
	tmpl *template.Template
}

// NewTemplates parses all templates from the given filesystem.
func NewTemplates(fsys fs.FS) (*Templates, error) {
	funcMap := template.FuncMap{
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
		"mul": func(a, b int) int { return a * b },
		"div": func(a, b int) int {
			if b == 0 {
				return 0
			}
			return a / b
		},
		"safeURL": func(s string) template.URL { return template.URL(s) },
		"safeHTML": func(s string) template.HTML { return template.HTML(s) },
		"pct": func(count, total int) int {
			if total == 0 {
				return 0
			}
			return count * 100 / total
		},
		"div100": func(total, value int) int {
			if total == 0 {
				return 0
			}
			return value / total
		},
		"initial": func(s string) string {
			for _, r := range s {
				return strings.ToUpper(string(r))
			}
			return ""
		},
	}

	tmpl, err := template.New("").Funcs(funcMap).ParseFS(fsys, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Templates{tmpl: tmpl}, nil
}

// Render renders a named template to the response writer.
func (t *Templates) Render(w http.ResponseWriter, name string, data interface{}) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("template %s error: %v", name, err)
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
	}
}
