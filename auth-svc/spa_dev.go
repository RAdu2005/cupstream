//go:build dev

package main

import (
	"io/fs"
	"os"
)

func distFSys() fs.FS {
	return os.DirFS("./dist")
}
