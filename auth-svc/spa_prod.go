//go:build !dev

package main

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

func distFSys() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic("auth-svc: dist subdir missing from embed")
	}
	return sub
}
