package websearch

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

const (
	// downloadHTTPTimeout bounds a whole file download. Large transfers need
	// far longer than defaultHTTPTimeout (30s) used by search/fetch, so the
	// download callable gets a dedicated client (a.downloadHTTP).
	downloadHTTPTimeout = 5 * time.Minute
	// defaultMaxDownloadBytes caps a download at 500 MB when the caller does
	// not set MaxBytes, so a runaway URL cannot fill the disk.
	defaultMaxDownloadBytes int64 = 500 * 1024 * 1024
)

// handleDownload streams a file from req.URL to req.SavePath on disk using the
// dedicated long-timeout download client. The download stops at MaxBytes
// (default 500 MB); a larger body is truncated at the cap and reported with
// Truncated=true. The parent directory of SavePath is created on demand.
//
// Stateless (PureContext): the copy can block for downloadHTTPTimeout (5min),
// which would starve every other websearch callable behind the owner lane
// (constraints "Owner Lane 禁阻塞"). It touches no shared mutable state —
// only the immutable a.downloadHTTP client and local files — so concurrent
// downloads/fetches/searches run on separate goroutines. Concurrent downloads
// to the same SavePath are last-writer-wins, which matches the caller model
// (one file per tool call).
func (a *Actor) handleDownload(_ actor.PureContext, req gen.WebDownloadReq) (gen.WebDownloadResp, error) {
	url := strings.TrimSpace(req.URL)
	if url == "" {
		return gen.WebDownloadResp{}, fmt.Errorf("websearch.download: url is required")
	}
	savePath := strings.TrimSpace(req.SavePath)
	if savePath == "" {
		return gen.WebDownloadResp{}, fmt.Errorf("websearch.download: savePath is required")
	}

	resp, err := a.downloadHTTP.Get(url)
	if err != nil {
		return gen.WebDownloadResp{}, fmt.Errorf("websearch.download: GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return gen.WebDownloadResp{}, fmt.Errorf("websearch.download: GET %s returned HTTP %d", url, resp.StatusCode)
	}

	maxBytes := int64(req.MaxBytes)
	if maxBytes <= 0 {
		maxBytes = defaultMaxDownloadBytes
	}

	if dir := filepath.Dir(savePath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return gen.WebDownloadResp{}, fmt.Errorf("websearch.download: create dir %s: %w", dir, err)
		}
	}

	f, err := os.Create(savePath)
	if err != nil {
		return gen.WebDownloadResp{}, fmt.Errorf("websearch.download: create file %s: %w", savePath, err)
	}
	defer f.Close()

	written, err := io.Copy(f, io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return gen.WebDownloadResp{}, fmt.Errorf("websearch.download: copy body: %w", err)
	}

	// Hit the cap: probe one more byte to tell "exactly MaxBytes" apart from
	// "body was larger" (Content-Length is unreliable for chunked bodies).
	truncated := false
	if written >= maxBytes {
		var probe [1]byte
		if _, err := io.ReadFull(resp.Body, probe[:]); err == nil {
			truncated = true
		}
	}

	return gen.WebDownloadResp{
		SavedPath:       savePath,
		BytesDownloaded: int32(written),
		ContentType:     resp.Header.Get("Content-Type"),
		Truncated:       truncated,
	}, nil
}
