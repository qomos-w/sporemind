package sshmanager

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
	"github.com/qomos-w/sporemind/pkg/archive"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// tempArchivePattern is the fixed filename template for UUID-named transfer
// archives created in the actor's local OS temp directory.
const tempArchivePattern = ".sporecode-transfer-%s.tar.gz"

// transferTempName returns a UUID-named temp archive path under the OS temp
// directory. The caller owns the file's lifetime and must remove it.
func transferTempName() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf(tempArchivePattern, uuid.NewString()))
}

// newTransferTempFile creates an exclusive (O_EXCL) temp archive file with a
// fresh UUID name, retrying on the astronomically unlikely collision. The file
// is created with 0600 perms so it is not world-readable while it exists.
// This mirrors the project actor's implementation for collision-safe temp
// archive lifecycle.
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
	return nil, fmt.Errorf("sshmanager: could not create unique temp archive file")
}

// ---------------------------------------------------------------------------
// Archive export: remote file or directory → tar.gz → base64
// ---------------------------------------------------------------------------

func (a *Actor) handleArchiveExport(ctx actor.PureContext, req domain.SshArchiveExportReq) (domain.ArchiveExportResp, error) {
	fs, err := a.getFilesystem(ctx, req.SessionID)
	if err != nil {
		return domain.ArchiveExportResp{}, err
	}
	defer fs.Close()

	sourcePath := path.Clean(req.Path)
	if sourcePath == "" || sourcePath == "." {
		return domain.ArchiveExportResp{}, fmt.Errorf("source path is required")
	}

	// Stat the source to determine its kind.
	_, _, isDir, modeStr, _, err := fs.Stat(sourcePath)
	if err != nil {
		return domain.ArchiveExportResp{}, fmt.Errorf("stat source: %w", err)
	}

	// Reject unsupported file kinds (symlinks, devices, sockets, pipes).
	// A regular file has mode "-..." in the SFTP backend; the exec/SCP backend
	// returns an empty mode string and we trust isDir=false.
	if !isDir && !isRegularFileMode(modeStr) {
		return domain.ArchiveExportResp{}, fmt.Errorf("unsupported file type for %q (mode %q)", sourcePath, modeStr)
	}

	// UUID-named temp archive on the actor's local filesystem, created
	// exclusively (O_EXCL) to guarantee no collision.
	f, err := newTransferTempFile()
	if err != nil {
		return domain.ArchiveExportResp{}, fmt.Errorf("create temp archive: %w", err)
	}
	// defer order matters on Windows (open files cannot be deleted): f.Close
	// runs before os.Remove because defers are LIFO and os.Remove was deferred
	// first.
	defer os.Remove(f.Name())
	defer f.Close()

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	var numEntries int
	var walkErr error
	if isDir {
		numEntries, walkErr = archiveRemoteTree(fs, sourcePath, tw)
	} else {
		numEntries, walkErr = archiveRemoteFile(fs, sourcePath, tw)
	}

	// Close in reverse order; preserve the first error. The file handle f is
	// NOT closed here — it stays open for the seek+read below and is closed by
	// the deferred f.Close().
	if cerr := tw.Close(); cerr != nil && walkErr == nil {
		walkErr = cerr
	}
	if cerr := gw.Close(); cerr != nil && walkErr == nil {
		walkErr = cerr
	}
	if walkErr != nil {
		return domain.ArchiveExportResp{}, fmt.Errorf("archive remote source: %w", walkErr)
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return domain.ArchiveExportResp{}, fmt.Errorf("seek temp archive: %w", err)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return domain.ArchiveExportResp{}, fmt.Errorf("read temp archive: %w", err)
	}

	if int64(len(data)) > archive.MaxCompressedSize {
		return domain.ArchiveExportResp{}, fmt.Errorf("compressed archive size %d exceeds limit %d", len(data), archive.MaxCompressedSize)
	}

	return domain.ArchiveExportResp{
		Content:    base64.StdEncoding.EncodeToString(data),
		Size:       int64(len(data)),
		NumEntries: int32(numEntries),
	}, nil
}

// isRegularFileMode reports whether the mode string from remoteFS.Stat
// represents a regular file. The SFTP backend produces Go FileMode strings
// like "-rw-r--r--" for regular files, "L..." for symlinks, "D..." for
// devices. The exec/SCP backend returns an empty string, so an empty mode
// with isDir=false is treated as a regular file.
func isRegularFileMode(mode string) bool {
	if mode == "" {
		return true
	}
	return strings.HasPrefix(mode, "-")
}

// archiveRemoteFile reads a single remote regular file and writes exactly one
// top-level tar entry named path.Base(sourcePath) with the raw bytes preserved.
// Returns 1 (one entry written).
func archiveRemoteFile(fs remoteFS, sourcePath string, tw *tar.Writer) (int, error) {
	content, err := fs.Read(sourcePath)
	if err != nil {
		return 0, fmt.Errorf("read remote file %q: %w", sourcePath, err)
	}
	base := path.Base(path.Clean(sourcePath))
	if err := archive.WriteEntry(tw, archive.Entry{
		Name:    base,
		Mode:    0644,
		Content: content,
	}); err != nil {
		return 0, fmt.Errorf("write file entry %q: %w", base, err)
	}
	return 1, nil
}

// archiveRemoteTree recursively walks the remote directory tree rooted at
// sourceDir and writes tar entries (one per file/directory) to tw. The source
// directory's base name is included as a top-level directory entry so that
// directory identity is preserved on transfer (exporting /foo produces entries
// foo/, foo/child.txt, etc.). Files are read one at a time via fs.Read so that
// only one file's content is in memory at any moment. Returns the total number
// of entries written.
func archiveRemoteTree(fs remoteFS, sourceDir string, tw *tar.Writer) (int, error) {
	base := path.Base(path.Clean(sourceDir))
	// Write the source root as a top-level directory entry.
	if err := archive.WriteEntry(tw, archive.Entry{
		Name:  base + "/",
		Mode:  0755,
		IsDir: true,
	}); err != nil {
		return 0, fmt.Errorf("write root dir entry %q: %w", base, err)
	}
	n, err := writeRemoteDir(fs, sourceDir, base, tw)
	return n + 1, err
}

// writeRemoteDir recursively lists dir and writes each child as a tar entry
// prefixed with prefix (the accumulated archive path up to and including dir).
func writeRemoteDir(fs remoteFS, dir, prefix string, tw *tar.Writer) (int, error) {
	entries, err := fs.List(dir)
	if err != nil {
		return 0, fmt.Errorf("list %q: %w", dir, err)
	}
	count := 0
	for _, e := range entries {
		if e.Name == "." || e.Name == ".." {
			continue
		}
		name := path.Join(prefix, e.Name)
		if e.IsDir {
			if err := archive.WriteEntry(tw, archive.Entry{
				Name:  name + "/",
				Mode:  0755,
				IsDir: true,
			}); err != nil {
				return count, fmt.Errorf("write dir entry %q: %w", name, err)
			}
			count++
			n, err := writeRemoteDir(fs, e.FullPath, name, tw)
			count += n
			if err != nil {
				return count, err
			}
		} else {
			content, err := fs.Read(e.FullPath)
			if err != nil {
				return count, fmt.Errorf("read remote file %q: %w", e.FullPath, err)
			}
			if err := archive.WriteEntry(tw, archive.Entry{
				Name:    name,
				Mode:    0644,
				Content: content,
			}); err != nil {
				return count, fmt.Errorf("write file entry %q: %w", name, err)
			}
			count++
		}
	}
	return count, nil
}

// remoteRelPath computes the POSIX relative path from root to full, using
// forward slashes. full is expected to be under root.
func remoteRelPath(root, full string) string {
	root = path.Clean(root)
	full = path.Clean(full)
	if full == root {
		return "."
	}
	prefix := root + "/"
	if strings.HasPrefix(full, prefix) {
		return full[len(prefix):]
	}
	return full
}

// ---------------------------------------------------------------------------
// Archive import: base64 → tar.gz → safe extract → remote directory
// ---------------------------------------------------------------------------

func (a *Actor) handleArchiveImport(ctx actor.PureContext, req domain.SshArchiveImportReq) (domain.ArchiveImportResp, error) {
	fs, err := a.getFilesystem(ctx, req.SessionID)
	if err != nil {
		return domain.ArchiveImportResp{}, err
	}
	defer fs.Close()

	targetDir := path.Clean(req.Path)
	if targetDir == "" || targetDir == "." {
		return domain.ArchiveImportResp{}, fmt.Errorf("target path is required")
	}

	// Verify the target exists and is a directory before extraction.
	_, _, isDir, _, _, statErr := fs.Stat(targetDir)
	if statErr != nil {
		return domain.ArchiveImportResp{}, fmt.Errorf("stat target: %w", statErr)
	}
	if !isDir {
		return domain.ArchiveImportResp{}, fmt.Errorf("target path %q is not a directory", targetDir)
	}

	// Decode base64 content.
	data, err := base64.StdEncoding.DecodeString(req.Content)
	if err != nil {
		return domain.ArchiveImportResp{}, fmt.Errorf("decode base64 archive: %w", err)
	}
	if int64(len(data)) > archive.MaxCompressedSize {
		return domain.ArchiveImportResp{}, fmt.Errorf("compressed archive size %d exceeds limit %d", len(data), archive.MaxCompressedSize)
	}

	// Stage in a UUID-named temp file, created exclusively (O_EXCL).
	f, err := newTransferTempFile()
	if err != nil {
		return domain.ArchiveImportResp{}, fmt.Errorf("create temp archive: %w", err)
	}
	defer os.Remove(f.Name())
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return domain.ArchiveImportResp{}, fmt.Errorf("write temp archive: %w", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return domain.ArchiveImportResp{}, fmt.Errorf("seek temp archive: %w", err)
	}

	numEntries, bytesWritten, err := extractToRemote(fs, targetDir, f)
	if err != nil {
		return domain.ArchiveImportResp{}, err
	}

	return domain.ArchiveImportResp{
		NumEntries:   int32(numEntries),
		BytesWritten: bytesWritten,
	}, nil
}

// extractToRemote safely extracts a gzip-compressed tar archive from r into
// targetDir on the remote filesystem via fs. Only regular files and
// directories are created; symlink/hardlink and traversal entries are rejected
// by the shared archive.Extract validation.
func extractToRemote(fs remoteFS, targetDir string, r io.Reader) (int, int64, error) {
	handlers := archive.ExtractHandlers{
		Mkdir: func(fullPath string) error {
			return fs.Mkdir(fullPath)
		},
		WriteFile: func(fullPath string, content []byte) error {
			return fs.Write(fullPath, content)
		},
	}
	return archive.Extract(r, targetDir, handlers)
}
