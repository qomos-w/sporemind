package installer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// httpDownload streams the artifact at rawURL into destPath, verifying the
// SHA256 digest before the destination becomes visible. The body is written
// to a .part sibling and renamed only after the full digest matches, so a
// truncated or tampered download never leaves a usable artifact behind —
// even inside private staging.
//
// progress (may be nil) is invoked after every chunk with the running byte
// count; total comes from Content-Length (-1 when unknown). ctx cancellation
// is checked between chunks so an aborted install stops promptly.
func httpDownload(ctx context.Context, client *http.Client, rawURL, destPath, wantSHA string, maxBytes int64, progress ProgressFunc) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", rawURL, resp.StatusCode)
	}

	partPath := destPath + ".part"
	f, err := os.Create(partPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", partPath, err)
	}

	hasher := sha256.New()
	buf := make([]byte, 32<<10)
	var done int64
	total := resp.ContentLength // -1 when unknown

	for {
		if err := ctx.Err(); err != nil {
			f.Close()
			os.Remove(partPath)
			return fmt.Errorf("download %s: %w", rawURL, err)
		}
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			done += int64(n)
			if maxBytes > 0 && done > maxBytes {
				f.Close()
				os.Remove(partPath)
				return fmt.Errorf("download %s: %d bytes exceeds limit %d", rawURL, done, maxBytes)
			}
			hasher.Write(buf[:n])
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				os.Remove(partPath)
				return fmt.Errorf("write %s: %w", partPath, werr)
			}
			if progress != nil {
				progress(done, total)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			f.Close()
			os.Remove(partPath)
			// Surface context cancellation even when it raced with a
			// transport-level read error.
			if ctxErr := ctx.Err(); ctxErr != nil {
				return fmt.Errorf("download %s: %w", rawURL, ctxErr)
			}
			return fmt.Errorf("read %s: %w", rawURL, readErr)
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(partPath)
		return fmt.Errorf("close %s: %w", partPath, err)
	}

	got := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(got, wantSHA) {
		os.Remove(partPath)
		return fmt.Errorf("sha256 mismatch for %s: got %s want %s", rawURL, got, wantSHA)
	}

	if err := os.Rename(partPath, destPath); err != nil {
		os.Remove(partPath)
		return fmt.Errorf("rename %s → %s: %w", partPath, destPath, err)
	}
	return nil
}
