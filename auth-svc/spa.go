package main

import (
	"io/fs"
	"net/http"
	"strings"
)

func spaHandler(fsys fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") ||
			strings.HasPrefix(r.URL.Path, "/live/") {
			http.NotFound(w, r)
			return
		}
		upath := strings.TrimPrefix(r.URL.Path, "/")
		if upath == "" {
			upath = "index.html"
		} else if i := strings.IndexAny(upath, "?#"); i >= 0 {
			upath = upath[:i]
		}
		if _, err := fs.Stat(fsys, upath); err != nil {
			upath = "index.html"
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/" + upath
		fileServer.ServeHTTP(w, r2)
	})
}
