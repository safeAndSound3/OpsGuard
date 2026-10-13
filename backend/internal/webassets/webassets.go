package webassets

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed assets/* index.html favicon.svg hadoop.svg icons.svg mysql.svg hadoop-elephant.png
var files embed.FS

func Handler() http.Handler {
	root, err := fs.Sub(files, ".")
	if err != nil {
		return http.NotFoundHandler()
	}
	fileServer := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if requestPath == "" {
			requestPath = "index.html"
		}
		if _, err := fs.Stat(root, requestPath); err != nil && !strings.Contains(path.Base(requestPath), ".") {
			r.URL.Path = "/index.html"
		}
		fileServer.ServeHTTP(w, r)
	})
}
