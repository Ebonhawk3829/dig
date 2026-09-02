package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static
var staticFS embed.FS

// staticHandler serves the embedded static assets under /static/.
func staticHandler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err) // embed failure is a compile-time problem
	}
	return http.StripPrefix("/static/", http.FileServer(http.FS(sub)))
}
