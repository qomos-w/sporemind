package web

import (
	"embed"
	"io/fs"
)

//go:embed dist
var embedded embed.FS

func Assets() fs.FS {
	return embedded
}
