package mobileassets

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServeDownloadServesEmbeddedAPK(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/download/mobile.apk", nil)
	rec := httptest.NewRecorder()

	ServeDownload(rec, req)

	// The default build embeds the real APK; the noapk build embeds a
	// placeholder that fails the magic check and must 404.
	if noapkBuild {
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for placeholder APK, got %d", rec.Code)
		}
		return
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for embedded APK, got %d", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("expected APK body")
	}
}