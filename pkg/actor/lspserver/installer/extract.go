package installer

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/qomos-w/sporemind/pkg/archive"
)

// unpack extracts the verified artifact into destDir according to the asset's
// kind. Extraction is defensive: only regular files and directories are
// accepted (symlinks and hard links are rejected), entry names must survive
// archive.SafeName validation, total bytes and entries are capped by limits,
// and file modes from the archive (including the executable bit) are
// preserved on Unix.
func unpack(artifactPath string, a Asset, destDir string, limits Limits) error {
	switch a.Kind {
	case KindTarGz:
		return unpackTarGz(artifactPath, destDir, a.StripTopLevel, limits)
	case KindZip:
		return unzip(artifactPath, destDir, a.StripTopLevel, limits)
	case KindRaw:
		destName := a.InstallName
		if destName == "" {
			destName = rawDestName(a.URL)
		}
		return installRaw(artifactPath, destName, destDir)
	case KindGzip:
		return unpackGzip(artifactPath, a.InstallName, destDir, limits)
	default:
		return fmt.Errorf("unsupported archive kind %q", a.Kind)
	}
}

// stripState tracks the common top-level segment when StripTopLevel is set.
type stripState struct {
	active bool
	prefix string // first path segment shared by every entry; "" until known
}

func (s *stripState) apply(cleanName string) (string, error) {
	if !s.active {
		return cleanName, nil
	}
	first, rest, _ := strings.Cut(cleanName, "/")
	if s.prefix == "" {
		s.prefix = first
	}
	if first != s.prefix {
		return "", fmt.Errorf("archive entries do not share a single top-level directory: %q vs %q", first, s.prefix)
	}
	if rest == "" {
		return ".", nil // the wrapper directory entry itself
	}
	return rest, nil
}

// unpackTarGz extracts a gzip-compressed tar archive into destDir.
func unpackTarGz(artifactPath, destDir string, strip bool, limits Limits) error {
	f, err := os.Open(artifactPath)
	if err != nil {
		return fmt.Errorf("open artifact: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat artifact: %w", err)
	}
	if info.Size() > limits.MaxCompressedBytes {
		return fmt.Errorf("artifact size %d exceeds limit %d", info.Size(), limits.MaxCompressedBytes)
	}

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("read gzip header: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	stripper := &stripState{active: strip}
	var written int64
	count := 0

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}
		switch hdr.Typeflag {
		case tar.TypeReg, tar.TypeRegA, tar.TypeDir:
			// allowed
		case tar.TypeSymlink, tar.TypeLink:
			return fmt.Errorf("symlink/hardlink entry rejected: %q", hdr.Name)
		default:
			return fmt.Errorf("unsupported tar entry type %d: %q", hdr.Typeflag, hdr.Name)
		}
		count++
		if count > limits.MaxEntries {
			return fmt.Errorf("archive exceeds max entries (%d)", limits.MaxEntries)
		}

		cleanName, err := archive.SafeName(hdr.Name)
		if err != nil {
			return err
		}
		cleanName, err = stripper.apply(cleanName)
		if err != nil {
			return err
		}
		if cleanName == "." || cleanName == "" {
			continue // wrapper dir entry collapses onto destDir itself
		}

		full := filepath.Join(destDir, filepath.FromSlash(cleanName))
		if !withinDir(destDir, full) {
			return fmt.Errorf("archive entry escapes target dir: %q", hdr.Name)
		}

		if hdr.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(full, 0o755); err != nil {
				return fmt.Errorf("mkdir %q: %w", full, err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("mkdir parent of %q: %w", full, err)
		}

		mode := os.FileMode(hdr.Mode) & 0o777
		if mode == 0 {
			mode = 0o644
		}
		n, err := writeFile(full, tr, mode, written, limits.MaxUncompressedBytes)
		if err != nil {
			return err
		}
		written += n
	}
	return nil
}

// unzip extracts a zip archive into destDir.
func unzip(artifactPath, destDir string, strip bool, limits Limits) error {
	zr, err := zip.OpenReader(artifactPath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()
	if len(zr.File) > limits.MaxEntries {
		return fmt.Errorf("archive has %d entries, exceeding limit %d", len(zr.File), limits.MaxEntries)
	}

	stripper := &stripState{active: strip}
	var written int64
	for _, zf := range zr.File {
		zfMode := zf.FileInfo().Mode()
		if zfMode&os.ModeSymlink != 0 || (!zf.FileInfo().IsDir() && !zfMode.IsRegular()) {
			return fmt.Errorf("non-regular zip entry rejected: %q", zf.Name)
		}
		written += int64(zf.UncompressedSize64)
		if written > limits.MaxUncompressedBytes {
			return fmt.Errorf("total uncompressed size %d exceeds limit %d", written, limits.MaxUncompressedBytes)
		}

		cleanName, err := archive.SafeName(zf.Name)
		if err != nil {
			return err
		}
		cleanName, err = stripper.apply(cleanName)
		if err != nil {
			return err
		}
		if cleanName == "." || cleanName == "" {
			continue
		}

		full := filepath.Join(destDir, filepath.FromSlash(cleanName))
		if !withinDir(destDir, full) {
			return fmt.Errorf("archive entry escapes target dir: %q", zf.Name)
		}

		if zf.FileInfo().IsDir() {
			if err := os.MkdirAll(full, 0o755); err != nil {
				return fmt.Errorf("mkdir %q: %w", full, err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("mkdir parent of %q: %w", full, err)
		}

		rc, err := zf.Open()
		if err != nil {
			return fmt.Errorf("open zip entry %q: %w", zf.Name, err)
		}
		mode := zfMode.Perm()
		if mode == 0 {
			mode = 0o644
		}
		_, err = writeFile(full, rc, mode, written-int64(zf.UncompressedSize64), limits.MaxUncompressedBytes)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// installRaw moves the downloaded bare file into destDir under destName with
// executable permissions (tools installed this way are binaries).
func installRaw(artifactPath, destName, destDir string) error {
	cleanName, err := archive.SafeName(destName)
	if err != nil {
		return fmt.Errorf("raw destination name: %w", err)
	}
	full := filepath.Join(destDir, filepath.FromSlash(cleanName))
	if !withinDir(destDir, full) {
		return fmt.Errorf("raw destination escapes target dir: %q", destName)
	}
	if err := os.Rename(artifactPath, full); err != nil {
		// Cross-device or platform-specific rename failure: fall back to copy.
		src, openErr := os.Open(artifactPath)
		if openErr != nil {
			return fmt.Errorf("open artifact: %w", openErr)
		}
		defer src.Close()
		if _, copyErr := writeFile(full, src, 0o755, 0, 1<<62); copyErr != nil {
			return copyErr
		}
		os.Remove(artifactPath)
	}
	if err := os.Chmod(full, 0o755); err != nil && !isWindowsNoOp(err) {
		return fmt.Errorf("chmod %q: %w", full, err)
	}
	return nil
}

// unpackGzip decompresses a single gzip-compressed file into destDir under
// destName, enforcing the uncompressed size cap and setting mode 0755.
func unpackGzip(artifactPath, destName, destDir string, limits Limits) error {
	f, err := os.Open(artifactPath)
	if err != nil {
		return fmt.Errorf("open artifact: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat artifact: %w", err)
	}
	if info.Size() > limits.MaxCompressedBytes {
		return fmt.Errorf("artifact size %d exceeds limit %d", info.Size(), limits.MaxCompressedBytes)
	}

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("read gzip header: %w", err)
	}
	defer gz.Close()

	clean, err := archive.SafeName(destName)
	if err != nil {
		return fmt.Errorf("gzip destination name: %w", err)
	}
	full := filepath.Join(destDir, filepath.FromSlash(clean))
	if !withinDir(destDir, full) {
		return fmt.Errorf("gzip destination escapes target dir: %q", destName)
	}

	if _, err := writeFile(full, gz, 0o755, 0, limits.MaxUncompressedBytes); err != nil {
		return err
	}
	return nil
}

// rawDestName derives the installed file name for a raw artifact from its
// URL base name, stripping any query string.
func rawDestName(rawURL string) string {
	base := path.Base(rawURL)
	if i := strings.IndexByte(base, '?'); i >= 0 {
		base = base[:i]
	}
	return base
}

// writeFile streams r into fullPath enforcing the running uncompressed-size
// cap, then applies mode via chmod (create with 0600 to avoid a writable-
// while-unprivileged window).
func writeFile(fullPath string, r io.Reader, mode os.FileMode, alreadyWritten, maxTotal int64) (int64, error) {
	f, err := os.OpenFile(fullPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, fmt.Errorf("create %q: %w", fullPath, err)
	}
	n, err := io.Copy(f, r)
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil && maxTotal-alreadyWritten-n < 0 {
		err = fmt.Errorf("total uncompressed size exceeds limit %d", maxTotal)
	}
	if err != nil {
		os.Remove(fullPath)
		return n, err
	}
	if err := os.Chmod(fullPath, mode); err != nil && !isWindowsNoOp(err) {
		os.Remove(fullPath)
		return n, fmt.Errorf("chmod %q: %w", fullPath, err)
	}
	return n, nil
}

// withinDir reports whether child resolves inside parent (defence in depth
// behind SafeName, guarding against platform-specific path quirks).
func withinDir(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// isWindowsNoOp reports whether a chmod error is expected noise on Windows,
// where permission bits are not enforced by the filesystem.
func isWindowsNoOp(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, os.ErrPermission) || strings.Contains(err.Error(), "not implemented")
}
