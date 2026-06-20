package main

import (
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
)

func spaHandler(fsys fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") ||
			strings.HasPrefix(r.URL.Path, "/live/") {
			http.NotFound(w, r)
			return
		}
		upath := strings.TrimPrefix(r.URL.Path, "/")
		if i := strings.IndexAny(upath, "?#"); i >= 0 {
			upath = upath[:i]
		}
		if upath == "" {
			upath = "index.html"
		}
		if _, err := fs.Stat(fsys, upath); err != nil {
			upath = "index.html"
		}
		f, err := fsys.Open(upath)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		stat, err := f.Stat()
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if stat.IsDir() {
			f.Close()
			upath = "index.html"
			f, err = fsys.Open(upath)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			defer f.Close()
			stat, err = f.Stat()
			if err != nil {
				http.NotFound(w, r)
				return
			}
		}
		if ctype := mime.TypeByExtension(filepath.Ext(upath)); ctype != "" {
			w.Header().Set("Content-Type", ctype)
		}
		if rs, ok := f.(io.ReadSeeker); ok {
			http.ServeContent(w, r, upath, stat.ModTime(), rs)
		} else {
			io.Copy(w, f)
		}
	})
}
