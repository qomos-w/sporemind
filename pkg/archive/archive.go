// Package archive provides stateless tar.gz encode/decode and safe entry
// validation for the directory archive transfer protocol shared by the
// project and sshmanager actors.
//
// This package holds no business state, no actor references, and no global
// singletons. It is a pure-logic utility — the actor-owned handlers in
// pkg/actor/project and pkg/actor/sshmanager call into it for the tar/gzip
// mechanics and path-traversal guards, then drive their own file-system I/O
// via callbacks.
package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"path"
	"strings"
)

// Size and count limits for safe archive transfer over JSON/base64 transport.
// These protect the actor and browser from memory exhaustion.
const (
	// MaxCompressedSize caps a single compressed archive.
	MaxCompressedSize int64 = 50 * 1024 * 1024 // 50 MiB
	// MaxUncompressedSize caps the total decompressed bytes during extraction.
	MaxUncompressedSize int64 = 500 * 1024 * 1024 // 500 MiB
	// MaxEntries caps the number of entries in a single archive.
	MaxEntries int = 10000
)

// Entry is one file or directory entry prepared for archiving.
type Entry struct {
	// Name is the POSIX path relative to the archive root, using forward
	// slashes. Directory names should include a trailing slash.
	Name string
	// Mode holds the permission bits (e.g. 0644, 0755).
	Mode int64
	// IsDir marks the entry as a directory.
	IsDir bool
	// Content is the file content. Ignored for directories.
	Content []byte
}

// WriteEntry writes a single entry's tar header (and content for regular
// files) to tw. It is the streaming primitive used by both Build and the
// actor-owned export walkers.
func WriteEntry(tw *tar.Writer, e Entry) error {
	if e.IsDir {
		name := e.Name
		if !strings.HasSuffix(name, "/") {
			name += "/"
		}
		hdr := &tar.Header{
			Name:     name,
			Mode:     e.Mode,
			Typeflag: tar.TypeDir,
			Format:   tar.FormatGNU,
		}
		return tw.WriteHeader(hdr)
	}
	hdr := &tar.Header{
		Name:     e.Name,
		Mode:     e.Mode,
		Size:     int64(len(e.Content)),
		Typeflag: tar.TypeReg,
		Format:   tar.FormatGNU,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if len(e.Content) == 0 {
		return nil
	}
	_, err := tw.Write(e.Content)
	return err
}

// Build writes a gzip-compressed tar archive of all entries to w. Returns the
// number of entries written. The caller is responsible for flushing/closing w
// after Build returns.
func Build(entries []Entry, w io.Writer) (int, error) {
	gw := gzip.NewWriter(w)
	tw := tar.NewWriter(gw)
	count := 0
	for _, e := range entries {
		if err := WriteEntry(tw, e); err != nil {
			_ = tw.Close()
			_ = gw.Close()
			return count, fmt.Errorf("write entry %q: %w", e.Name, err)
		}
		count++
	}
	if err := tw.Close(); err != nil {
		_ = gw.Close()
		return count, fmt.Errorf("close tar writer: %w", err)
	}
	if err := gw.Close(); err != nil {
		return count, fmt.Errorf("close gzip writer: %w", err)
	}
	return count, nil
}

// BuildBytes is a convenience wrapper that builds into an in-memory buffer.
func BuildBytes(entries []Entry) ([]byte, int, error) {
	var buf bytes.Buffer
	n, err := Build(entries, &buf)
	return buf.Bytes(), n, err
}

// SafeName validates a tar entry name against path-traversal attacks. It
// rejects absolute paths, ".." traversal components, NUL bytes, and backslash
// separators. Returns the cleaned relative name (forward slashes, no trailing
// slash) or an error.
func SafeName(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("archive entry name is empty")
	}
	if strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("archive entry name contains NUL byte: %q", name)
	}
	// Normalise backslashes to forward slashes so Windows-style paths cannot
	// bypass the component checks below.
	name = strings.ReplaceAll(name, "\\", "/")
	// Reject absolute paths.
	if strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("archive entry name is absolute: %q", name)
	}
	// Reject any ".." component before cleaning — we want to fail loudly, not
	// silently rewrite the path.
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", fmt.Errorf("archive entry name contains parent traversal: %q", name)
		}
	}
	cleaned := path.Clean(name)
	if cleaned == "." || cleaned == "" {
		return "", fmt.Errorf("archive entry name resolves to current directory: %q", name)
	}
	return cleaned, nil
}

// ContainsPath reports whether child is equal to or nested within parent after
// POSIX path cleaning. Used as defence-in-depth to verify extracted entries
// remain inside the target directory.
func ContainsPath(parent, child string) bool {
	parent = path.Clean(parent)
	child = path.Clean(child)
	if parent == child {
		return true
	}
	// Root "/" is a special case: parent+"/" produces "//", which no valid
	// path starts with. A root parent contains any absolute child.
	if parent == "/" {
		return strings.HasPrefix(child, "/")
	}
	return strings.HasPrefix(child, parent+"/")
}

// ExtractHandlers are the callbacks invoked by Extract for each validated
// entry. Both receive the full resolved path (guaranteed within targetDir).
type ExtractHandlers struct {
	// Mkdir creates a directory (and all ancestors). It must be idempotent.
	Mkdir func(fullPath string) error
	// WriteFile writes file content to fullPath. Parent directories are
	// guaranteed to exist when this is called.
	WriteFile func(fullPath string, content []byte) error
}

// Extract reads a gzip-compressed tar archive from r and safely extracts
// entries under targetDir. Only regular files and directories are accepted;
// symlinks, hard links, absolute paths, ".." traversal, NUL bytes, duplicate
// entries, and oversized archives are rejected. dirFn and fileFn are called
// for each directory/file entry. Returns the number of entries extracted and
// total uncompressed bytes written.
func Extract(r io.Reader, targetDir string, h ExtractHandlers) (numEntries int, bytesWritten int64, err error) {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return 0, 0, fmt.Errorf("read gzip header: %w", err)
	}
	tr := tar.NewReader(gr)

	targetDir = path.Clean(targetDir)
	seen := make(map[string]bool)
	createdDirs := make(map[string]bool)

	// Ensure the target directory itself exists.
	if !createdDirs[""] {
		if err := h.Mkdir(targetDir); err != nil {
			return 0, 0, fmt.Errorf("create target dir %q: %w", targetDir, err)
		}
		createdDirs[""] = true
	}

	for {
		hdr, rErr := tr.Next()
		if rErr == io.EOF {
			break
		}
		if rErr != nil {
			return numEntries, bytesWritten, fmt.Errorf("read tar entry: %w", rErr)
		}

		if numEntries >= MaxEntries {
			return numEntries, bytesWritten, fmt.Errorf("archive exceeds max entries (%d)", MaxEntries)
		}

		// Reject symlinks, hard links, and other non-regular types.
		switch hdr.Typeflag {
		case tar.TypeReg, tar.TypeRegA, tar.TypeDir:
			// allowed
		case tar.TypeSymlink, tar.TypeLink:
			return numEntries, bytesWritten, fmt.Errorf("symlink/hardlink entry rejected: %q", hdr.Name)
		default:
			return numEntries, bytesWritten, fmt.Errorf("unsupported tar entry type %d: %q", hdr.Typeflag, hdr.Name)
		}

		// Validate and clean the entry name.
		cleanName, vErr := SafeName(hdr.Name)
		if vErr != nil {
			return numEntries, bytesWritten, vErr
		}

		// Reject duplicate entries.
		if seen[cleanName] {
			return numEntries, bytesWritten, fmt.Errorf("duplicate archive entry: %q", cleanName)
		}
		seen[cleanName] = true

		// Compute full path and verify containment.
		full := path.Join(targetDir, cleanName)
		if !ContainsPath(targetDir, full) {
			return numEntries, bytesWritten, fmt.Errorf("archive entry escapes target dir: %q", hdr.Name)
		}

		if hdr.Typeflag == tar.TypeDir {
			// Create the directory (idempotent).
			if err := h.Mkdir(full); err != nil {
				return numEntries, bytesWritten, fmt.Errorf("mkdir %q: %w", full, err)
			}
			createdDirs[cleanName] = true
			numEntries++
			continue
		}

		// Regular file — ensure parent directory exists.
		parentRel := path.Dir(cleanName)
		if parentRel != "." && !createdDirs[parentRel] {
			parentFull := path.Join(targetDir, parentRel)
			if !ContainsPath(targetDir, parentFull) {
				return numEntries, bytesWritten, fmt.Errorf("file parent escapes target dir: %q", hdr.Name)
			}
			if err := h.Mkdir(parentFull); err != nil {
				return numEntries, bytesWritten, fmt.Errorf("mkdir parent %q: %w", parentFull, err)
			}
			createdDirs[parentRel] = true
		}

		// Read file content with uncompressed size accounting.
		content, rErr := io.ReadAll(tr)
		if rErr != nil {
			return numEntries, bytesWritten, fmt.Errorf("read file content %q: %w", full, rErr)
		}
		bytesWritten += int64(len(content))
		if bytesWritten > MaxUncompressedSize {
			return numEntries, bytesWritten, fmt.Errorf("total uncompressed size %d exceeds limit %d", bytesWritten, MaxUncompressedSize)
		}

		if err := h.WriteFile(full, content); err != nil {
			return numEntries, bytesWritten, fmt.Errorf("write file %q: %w", full, err)
		}
		numEntries++
	}

	return numEntries, bytesWritten, nil
}

// ExtractBytes is a convenience wrapper that extracts from an in-memory
// archive. It enforces the compressed size limit before extracting.
func ExtractBytes(data []byte, targetDir string, h ExtractHandlers) (int, int64, error) {
	if int64(len(data)) > MaxCompressedSize {
		return 0, 0, fmt.Errorf("compressed archive size %d exceeds limit %d", len(data), MaxCompressedSize)
	}
	return Extract(bytes.NewReader(data), targetDir, h)
}
