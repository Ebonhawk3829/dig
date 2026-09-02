package web

import (
	"html/template"
	"testing"
)

// TestTemplatesParse ensures all templates parse with the shared funcs.
func TestTemplatesParse(t *testing.T) {
	if _, err := template.New("dig").Funcs(templateFuncs).ParseFS(templates, "templates/*.html"); err != nil {
		t.Fatalf("templates failed to parse: %v", err)
	}
}
