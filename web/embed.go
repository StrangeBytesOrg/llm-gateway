package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:dist
var files embed.FS

// Handler serves the embedded Vite build. The build output is intentionally
// not committed; CI, make, and the Docker build generate it before compiling Go.
// A no-UI build can add a `!noui` build tag here plus a `noui.go` Handler stub.
func Handler() http.Handler {
	root, err := fs.Sub(files, "dist")
	if err != nil {
		return unavailable(err)
	}
	return handler(root)
}
