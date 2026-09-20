package project

import (
	"archive/tar"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
)

const (
	// maxArchiveCompressed caps the compressed tar.gz produced by export and
	// consumed by import. The archive crosses the RPC boundary as base64, so an
	// explicit cap prevents actor/browser memory exhaustion.
	maxArchiveCompressed int64 = 50 * 1024 * 1024
	// maxArchiveUncompressed caps the total decompressed bytes during import
	// (and total source file bytes during export), guarding against decompression
	// bombs regardless of the compressed size.
	maxArchiveUncompressed int64 = 500 * 1024 * 1024
	// maxArchiveEntries caps the number of tar entries in either direction.
	maxArchiveEntries = 10000
)

// transferTempName returns a UUID-named temp archive path under the OS temp
// directory. The fixed ".sporecode-transfer-" prefix and ".tar.gz" suffix make
// the files identifiable, and the random UUID guarantees no collision with user
// directory names or other in-flight transfers. The caller owns the file's
// lifetime and must remove it.
func transferTempName() string {
	return filepath.Join(os.TempDir(), ".sporecode-transfer-"+uuid.NewString()+".tar.gz")
}

// newTransferTempFile creates an exclusive (O_EXCL) temp archive file with a
// fresh UUID name, retrying on the astronomically unlikely collision. The file
// is created with 0600 perms so it is not world-readable while it exists.
func newTransferTempFile() (*os.File, error) {
	for i := 0; i < 3; i++ {
		f, err := os.OpenFile(transferTempName(), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err == nil {
			return f, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("project: could not create unique temp archive file")
}

// handleArchiveExport recursively packs the source directory into a tar.gz
// archive and returns it as base64 content. Only regular files and directories
// are carried; symlinks and other special files are skipped. The archive is
// streamed into a UUID-named temp file that is removed before return. Paths are
// stored relative to the source directory's parent (so the top-level entry is
// the source directory's base name), preserving directory identity on transfer.
func (a *Actor) handleArchiveExport(ctx actor.PureContext, req domain.ArchiveExportReq) (domain.ArchiveExportResp, error) {
	srcPath, err := a.resolveBrowsePathFor(callerID(ctx), req.Path)
	if err != nil {
		return domain.ArchiveExportResp{}, err
	}
	info, err := os.Stat(srcPath)
	if err != nil {
		return domain.ArchiveExportResp{}, fmt.Errorf("project.archive_export: %w", err)
	}
	// Only regular files and directories are archivable. Devices, sockets,
	// named pipes and other special files are rejected up front.
	mode := info.Mode()
	if !mode.IsRegular() && !mode.IsDir() {
		return domain.ArchiveExportResp{}, fmt.Errorf("project.archive_export: source path is not a regular file or directory: %s", req.Path)
	}

	f, err := newTransferTempFile()
	if err != nil {
		return domain.ArchiveExportResp{}, err
	}
	// defer order matters on Windows (open files cannot be deleted): f.Close
	// runs before os.Remove because defers are LIFO and os.Remove was deferred
	// first.
	defer os.Remove(f.Name())
	defer f.Close()

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	var numEntries int
	var totalSize int64

	if mode.IsRegular() {
		// Single regular file: the archive contains exactly one top-level
		// entry named path.Base(sourcePath), preserving arbitrary binary bytes
		// (including NUL).
		numEntries = 1
		if err := writeSingleFileEntry(tw, srcPath, info, &totalSize); err != nil {
			_ = tw.Close()
			_ = gw.Close()
			return domain.ArchiveExportResp{}, err
		}
	} else {
		base := filepath.Base(filepath.Clean(srcPath))
		n, err := packDirToTar(tw, srcPath, base, &totalSize)
		if err != nil {
			// Best-effort close of the writers; the temp file is removed by
			// defer regardless. Unclosed writers are harmless since the file
			// is deleted.
			_ = tw.Close()
			_ = gw.Close()
			return domain.ArchiveExportResp{}, err
		}
		numEntries = n
	}
	if err := tw.Close(); err != nil {
		_ = gw.Close()
		return domain.ArchiveExportResp{}, fmt.Errorf("project.archive_export: close tar writer: %w", err)
	}
	if err := gw.Close(); err != nil {
		return domain.ArchiveExportResp{}, fmt.Errorf("project.archive_export: close gzip writer: %w", err)
	}

	fi, err := f.Stat()
	if err != nil {
		return domain.ArchiveExportResp{}, fmt.Errorf("project.archive_export: stat temp: %w", err)
	}
	if fi.Size() > maxArchiveCompressed {
		return domain.ArchiveExportResp{}, fmt.Errorf("project.archive_export: compressed size %d exceeds %d byte limit", fi.Size(), maxArchiveCompressed)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return domain.ArchiveExportResp{}, fmt.Errorf("project.archive_export: seek temp: %w", err)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return domain.ArchiveExportResp{}, fmt.Errorf("project.archive_export: read temp: %w", err)
	}
	return domain.ArchiveExportResp{
		Content:    base64.StdEncoding.EncodeToString(data),
		Size:       fi.Size(),
		NumEntries: int32(numEntries),
	}, nil
}

// packDirToTar recursively packs srcDir into tw. Entries are stored relative to
// srcDir's parent so the top-level entry is base (= filepath.Base(srcDir)),
// preserving directory identity on transfer. Only regular files and directories
// are carried; symlinks and other non-regular specials are skipped, and the
// entry count and uncompressed byte limits are enforced. It returns the number
// of entries written. This is the directory path of archive export; the logic
// is unchanged from the original inline walk.
func packDirToTar(tw *tar.Writer, srcDir, base string, totalSize *int64) (int, error) {
	var numEntries int
	walkErr := filepath.WalkDir(srcDir, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		// Skip symlinks and other non-regular specials: they cannot be carried
		// safely across filesystems and the extraction validator rejects them
		// anyway. WalkDir does not follow symlinks, so this just drops them.
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		numEntries++
		if numEntries > maxArchiveEntries {
			return fmt.Errorf("project.archive_export: archive exceeds %d entry limit", maxArchiveEntries)
		}
		fi, err := d.Info()
		if err != nil {
			return fmt.Errorf("project.archive_export: stat %s: %w", p, err)
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return fmt.Errorf("project.archive_export: rel %s: %w", p, err)
		}
		name := path.Clean(filepath.ToSlash(filepath.Join(base, rel)))
		hdr := &tar.Header{
			Name:    name,
			ModTime: fi.ModTime(),
			Mode:    int64(fi.Mode().Perm()),
		}
		switch {
		case fi.IsDir():
			hdr.Typeflag = tar.TypeDir
			hdr.Name = name + "/"
			if err := tw.WriteHeader(hdr); err != nil {
				return fmt.Errorf("project.archive_export: write dir header %s: %w", p, err)
			}
		default:
			// Only regular files reach here (symlinks were skipped above;
			// WalkDir reports devices/fifos/sockets with non-zero ModeType bits
			// and they are not regular — skip them defensively).
			if !fi.Mode().IsRegular() {
				return nil
			}
			if fi.Size() > 0 {
				*totalSize += fi.Size()
				if *totalSize > maxArchiveUncompressed {
					return fmt.Errorf("project.archive_export: archive exceeds %d byte uncompressed limit", maxArchiveUncompressed)
				}
			}
			hdr.Typeflag = tar.TypeReg
			hdr.Size = fi.Size()
			if err := tw.WriteHeader(hdr); err != nil {
				return fmt.Errorf("project.archive_export: write header %s: %w", p, err)
			}
			if fi.Size() > 0 {
				rf, err := os.Open(p)
				if err != nil {
					return fmt.Errorf("project.archive_export: open %s: %w", p, err)
				}
				_, copyErr := io.Copy(tw, rf)
				rf.Close()
				if copyErr != nil {
					return fmt.Errorf("project.archive_export: copy %s: %w", p, copyErr)
				}
			}
		}
		return nil
	})
	if walkErr != nil {
		return 0, walkErr
	}
	return numEntries, nil
}

// writeSingleFileEntry writes exactly one regular-file tar entry to tw, named
// path.Base(absPath). It preserves arbitrary binary content byte-for-byte
// (including NUL bytes) and enforces the uncompressed size limit. The caller
// must have already verified fi is a regular file.
func writeSingleFileEntry(tw *tar.Writer, absPath string, fi os.FileInfo, totalSize *int64) error {
	name := path.Clean(filepath.ToSlash(filepath.Base(filepath.Clean(absPath))))
	if fi.Size() > 0 {
		*totalSize += fi.Size()
		if *totalSize > maxArchiveUncompressed {
			return fmt.Errorf("project.archive_export: archive exceeds %d byte uncompressed limit", maxArchiveUncompressed)
		}
	}
	hdr := &tar.Header{
		Name:     name,
		ModTime:  fi.ModTime(),
		Mode:     int64(fi.Mode().Perm()),
		Typeflag: tar.TypeReg,
		Size:     fi.Size(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("project.archive_export: write header %s: %w", absPath, err)
	}
	if fi.Size() > 0 {
		rf, err := os.Open(absPath)
		if err != nil {
			return fmt.Errorf("project.archive_export: open %s: %w", absPath, err)
		}
		_, copyErr := io.Copy(tw, rf)
		rf.Close()
		if copyErr != nil {
			return fmt.Errorf("project.archive_export: copy %s: %w", absPath, copyErr)
		}
	}
	return nil
}

// handleArchiveImport safely extracts a base64 tar.gz archive into a target
// directory. It rejects symlinks, hard links, absolute paths, NUL bytes,
// backslashes, ".." traversal, duplicate entries, oversized archives, and
// conflicts with existing regular files (safe merge). The base64 payload is
// decoded into a UUID-named temp file that is removed before return; extraction
// streams from that file to keep peak memory bounded.
func (a *Actor) handleArchiveImport(ctx actor.PureContext, req domain.ArchiveImportReq) (domain.ArchiveImportResp, error) {
	targetDir, err := a.resolveAbsolutePathConfirmFor(callerID(ctx), req.Path, false)
	if err != nil {
		return domain.ArchiveImportResp{}, err
	}
	info, err := os.Stat(targetDir)
	if err != nil {
		return domain.ArchiveImportResp{}, fmt.Errorf("project.archive_import: %w", err)
	}
	if !info.IsDir() {
		return domain.ArchiveImportResp{}, fmt.Errorf("project.archive_import: target path is not a directory: %s", req.Path)
	}

	raw, err := base64.StdEncoding.DecodeString(req.Content)
	if err != nil {
		return domain.ArchiveImportResp{}, fmt.Errorf("project.archive_import: decode base64: %w", err)
	}
	if int64(len(raw)) > maxArchiveCompressed {
		return domain.ArchiveImportResp{}, fmt.Errorf("project.archive_import: compressed size %d exceeds %d byte limit", len(raw), maxArchiveCompressed)
	}

	f, err := newTransferTempFile()
	if err != nil {
		return domain.ArchiveImportResp{}, err
	}
	defer os.Remove(f.Name())
	defer f.Close()

	if _, err := f.Write(raw); err != nil {
		return domain.ArchiveImportResp{}, fmt.Errorf("project.archive_import: write temp: %w", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return domain.ArchiveImportResp{}, fmt.Errorf("project.archive_import: seek temp: %w", err)
	}

	gz, err := gzip.NewReader(f)
	if err != nil {
		return domain.ArchiveImportResp{}, fmt.Errorf("project.archive_import: open gzip: %w", err)
	}
	tr := tar.NewReader(gz)

	absTarget, err := filepath.Abs(targetDir)
	if err != nil {
		return domain.ArchiveImportResp{}, fmt.Errorf("project.archive_import: resolve target: %w", err)
	}
	absTarget = filepath.Clean(absTarget)
	sep := string(filepath.Separator)

	var numEntries int
	var bytesWritten int64
	seen := make(map[string]bool)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return domain.ArchiveImportResp{}, fmt.Errorf("project.archive_import: read tar entry: %w", err)
		}
		cleaned, err := sanitizeArchiveEntry(hdr.Name)
		if err != nil {
			return domain.ArchiveImportResp{}, fmt.Errorf("project.archive_import: %w", err)
		}
		if cleaned == "" {
			// Root "." entry — nothing materializes on disk.
			continue
		}

		// Only regular files and directories are permitted.
		switch hdr.Typeflag {
		case tar.TypeDir, tar.TypeReg, tar.TypeRegA:
			// allowed
		default:
			return domain.ArchiveImportResp{}, fmt.Errorf("project.archive_import: rejecting entry %q: only regular files and directories are allowed (type %d)", hdr.Name, hdr.Typeflag)
		}

		if seen[cleaned] {
			return domain.ArchiveImportResp{}, fmt.Errorf("project.archive_import: duplicate entry %q", hdr.Name)
		}
		seen[cleaned] = true

		numEntries++
		if numEntries > maxArchiveEntries {
			return domain.ArchiveImportResp{}, fmt.Errorf("project.archive_import: archive exceeds %d entry limit", maxArchiveEntries)
		}

		dest := filepath.Clean(filepath.Join(absTarget, filepath.FromSlash(cleaned)))
		// Containment check: dest must stay within the target directory.
		if dest != absTarget && !strings.HasPrefix(dest, absTarget+sep) {
			return domain.ArchiveImportResp{}, fmt.Errorf("project.archive_import: entry %q escapes target directory", hdr.Name)
		}

		if hdr.Typeflag == tar.TypeDir {
			if existing, statErr := os.Lstat(dest); statErr == nil && !existing.IsDir() {
				return domain.ArchiveImportResp{}, fmt.Errorf("project.archive_import: cannot create directory %q: a file already exists", hdr.Name)
			}
			mode := dirMode(hdr.Mode)
			if err := os.MkdirAll(dest, mode); err != nil {
				return domain.ArchiveImportResp{}, fmt.Errorf("project.archive_import: mkdir %q: %w", hdr.Name, err)
			}
			continue
		}

		// Regular file.
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return domain.ArchiveImportResp{}, fmt.Errorf("project.archive_import: mkdir parent for %q: %w", hdr.Name, err)
		}
		// Safe merge: refuse to overwrite an existing regular file.
		if existing, statErr := os.Lstat(dest); statErr == nil {
			if existing.IsDir() {
				return domain.ArchiveImportResp{}, fmt.Errorf("project.archive_import: cannot create file %q: a directory already exists", hdr.Name)
			}
			return domain.ArchiveImportResp{}, fmt.Errorf("project.archive_import: refusing to overwrite existing file %q", hdr.Name)
		}
		if hdr.Size > 0 {
			bytesWritten += hdr.Size
			if bytesWritten > maxArchiveUncompressed {
				return domain.ArchiveImportResp{}, fmt.Errorf("project.archive_import: archive exceeds %d byte uncompressed limit", maxArchiveUncompressed)
			}
		}
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_EXCL, fileMode(hdr.Mode))
		if err != nil {
			return domain.ArchiveImportResp{}, fmt.Errorf("project.archive_import: create %q: %w", hdr.Name, err)
		}
		n, wErr := io.Copy(out, tr)
		cerr := out.Close()
		if wErr != nil {
			return domain.ArchiveImportResp{}, fmt.Errorf("project.archive_import: write %q: %w", hdr.Name, wErr)
		}
		if cerr != nil {
			return domain.ArchiveImportResp{}, fmt.Errorf("project.archive_import: close %q: %w", hdr.Name, cerr)
		}
		if n != hdr.Size {
			bytesWritten += n - hdr.Size
		}
	}

	return domain.ArchiveImportResp{
		NumEntries:   int32(numEntries),
		BytesWritten: bytesWritten,
	}, nil
}

// sanitizeArchiveEntry validates a tar entry name (POSIX forward-slash path)
// and returns its cleaned form. It rejects NUL bytes, backslashes, absolute
// paths (POSIX leading "/" or a Windows drive letter), and any ".." component
// that would traverse above the archive root. An empty result means the entry
// is the archive root itself and extracts to nothing. It deliberately uses the
// POSIX path package so validation is identical on Windows and POSIX hosts.
func sanitizeArchiveEntry(name string) (string, error) {
	if name == "" || name == "." || name == "/" || name == "./" {
		return "", nil
	}
	if strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("entry %q contains a NUL byte", name)
	}
	if strings.ContainsRune(name, '\\') {
		return "", fmt.Errorf("entry %q contains a backslash", name)
	}
	if strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("entry %q is an absolute path", name)
	}
	if len(name) >= 2 && name[1] == ':' {
		return "", fmt.Errorf("entry %q is an absolute path", name)
	}
	cleaned := path.Clean(name)
	if cleaned == "." {
		return "", nil
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("entry %q traverses outside the archive root", name)
	}
	return cleaned, nil
}

// dirMode normalizes a tar header mode into a usable directory permission,
// falling back to 0755 for zero or missing bits.
func dirMode(mode int64) os.FileMode {
	m := os.FileMode(mode).Perm()
	if m == 0 {
		return 0755
	}
	return m
}

// fileMode normalizes a tar header mode into a usable file permission, falling
// back to 0644 for zero or missing bits.
func fileMode(mode int64) os.FileMode {
	m := os.FileMode(mode).Perm()
	if m == 0 {
		return 0644
	}
	return m
}
