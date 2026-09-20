// Package mobileassets embeds the compiled Android APK into the sporemind
// binary and exposes a versioned download endpoint.
//
// The embed is build-tag conditional: default builds require the real
// sporemind.apk (produced by make build-apk / copy-mobile-asset); builds with
// the "noapk" tag embed a tiny placeholder instead, so lightweight targets
// (dev-release SDK-embed verification) neither ship the multi-MB APK nor
// trigger the gradle build chain. ServeDownload 404s on the placeholder.
package mobileassets

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"

	"github.com/qomos-w/sporemind/pkg/version"
)

// apkFS is the concrete embedded-APK type; declared here so both tag files
// assign the same var kind.
type apkFS = embed.FS

// Assets returns the embedded APK filesystem. The `embedded` var is declared
// in the build-tag pair mobileassets_apk.go / mobileassets_noapk.go.
func Assets() fs.FS {
	return embedded
}

// ServeDownload serves the embedded APK with a versioned filename.
// If the APK is missing or not a valid ZIP/APK, it returns 404.
func ServeDownload(w http.ResponseWriter, r *http.Request) {
	data, err := embedded.ReadFile("sporemind.apk")
	if err != nil || len(data) == 0 || !isAPK(data) {
		http.NotFound(w, r)
		return
	}

	filename := fmt.Sprintf("sporemind-v%s.apk", version.Version)
	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

func isAPK(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	// APK is a ZIP archive; valid local file headers start with PK\x03\x04.
	return string(data[:4]) == "PK\x03\x04"
}
