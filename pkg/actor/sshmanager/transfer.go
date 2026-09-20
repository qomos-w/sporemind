package sshmanager

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/archive"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// downloadFromFS reads a remote file or directory and returns its content as
// base64. For a regular file, the raw bytes are base64-encoded. For a directory,
// the tree is archived into a tar.gz and base64-encoded. The isDirectory flag in
// the result tells the caller which path was taken. Single files are capped at
// maxDownloadBytes; directories are capped at archive.MaxCompressedSize.
func downloadFromFS(fs remoteFS, sourcePath string) (name string, content string, size int64, isDir bool, numEntries int, err error) {
	sourcePath = path.Clean(sourcePath)
	if sourcePath == "" || sourcePath == "." {
		return "", "", 0, false, 0, fmt.Errorf("source path is required")
	}

	_, _, dir, modeStr, _, statErr := fs.Stat(sourcePath)
	if statErr != nil {
		return "", "", 0, false, 0, fmt.Errorf("stat source: %w", statErr)
	}

	if !dir && !isRegularFileMode(modeStr) {
		return "", "", 0, false, 0, fmt.Errorf("unsupported file type for %q (mode %q)", sourcePath, modeStr)
	}

	if dir {
		b64, archiveSize, n, exportErr := archiveRemoteToBase64(fs, sourcePath)
		if exportErr != nil {
			return "", "", 0, true, 0, exportErr
		}
		return path.Base(sourcePath), b64, archiveSize, true, n, nil
	}

	data, readErr := fs.Read(sourcePath)
	if readErr != nil {
		return "", "", 0, false, 0, fmt.Errorf("read file: %w", readErr)
	}
	if int64(len(data)) > maxDownloadBytes {
		return "", "", 0, false, 0, fmt.Errorf("file is %d bytes; download limit is %d bytes", len(data), maxDownloadBytes)
	}
	return path.Base(sourcePath), base64.StdEncoding.EncodeToString(data), int64(len(data)), false, 0, nil
}

// uploadToFS writes base64-encoded content to a remote path. When isArchive is
// true, the content is decoded as a tar.gz and extracted into targetDir (the
// directory is auto-created if it does not exist). When false, the content is
// decoded as raw bytes and written to the target file path (parent directories
// are auto-created).
func uploadToFS(fs remoteFS, targetPath string, b64Content string, isArchive bool) (numEntries int, bytesWritten int64, err error) {
	targetPath = path.Clean(targetPath)
	if targetPath == "" || targetPath == "." {
		return 0, 0, fmt.Errorf("target path is required")
	}

	data, derr := base64.StdEncoding.DecodeString(b64Content)
	if derr != nil {
		return 0, 0, fmt.Errorf("decode base64 content: %w", derr)
	}

	if isArchive {
		if int64(len(data)) > archive.MaxCompressedSize {
			return 0, 0, fmt.Errorf("compressed archive size %d exceeds limit %d", len(data), archive.MaxCompressedSize)
		}
		if _, _, isDir, _, _, statErr := fs.Stat(targetPath); statErr == nil && !isDir {
			return 0, 0, fmt.Errorf("target path %q exists and is not a directory", targetPath)
		} else if statErr != nil {
			if mkdirErr := fs.Mkdir(targetPath); mkdirErr != nil {
				return 0, 0, fmt.Errorf("create target directory: %w", mkdirErr)
			}
		}
		return extractToRemoteFromBytes(fs, targetPath, data)
	}

	parent := path.Dir(targetPath)
	if parent != "" && parent != "." && parent != "/" {
		if _, _, _, _, _, statErr := fs.Stat(parent); statErr != nil {
			if mkdirErr := fs.Mkdir(parent); mkdirErr != nil {
				return 0, 0, fmt.Errorf("create parent directory: %w", mkdirErr)
			}
		}
	}
	if writeErr := fs.Write(targetPath, data); writeErr != nil {
		return 0, 0, fmt.Errorf("write file: %w", writeErr)
	}
	return 0, int64(len(data)), nil
}

// extractToRemoteFromBytes extracts a gzip-compressed tar archive from a byte
// slice into targetDir on the remote filesystem. Wrapper around extractToRemote
// that accepts bytes instead of an io.Reader for convenience.
func extractToRemoteFromBytes(fs remoteFS, targetDir string, data []byte) (int, int64, error) {
	return extractToRemote(fs, targetDir, bytes.NewReader(data))
}

// archiveRemoteToBase64 archives a remote file or directory into a tar.gz,
// reads the result, and returns it as a base64-encoded string. Mirrors
// handleArchiveExport's core sequence without actor context so it can be
// unit-tested with mockRemoteFS.
func archiveRemoteToBase64(fs remoteFS, sourcePath string) (b64 string, size int64, numEntries int, err error) {
	f, cerr := newTransferTempFile()
	if cerr != nil {
		return "", 0, 0, fmt.Errorf("create temp archive: %w", cerr)
	}
	defer os.Remove(f.Name())
	defer f.Close()

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	_, _, dir, _, _, _ := fs.Stat(sourcePath)
	var n int
	var walkErr error
	if dir {
		n, walkErr = archiveRemoteTree(fs, sourcePath, tw)
	} else {
		n, walkErr = archiveRemoteFile(fs, sourcePath, tw)
	}
	if cerr := tw.Close(); cerr != nil && walkErr == nil {
		walkErr = cerr
	}
	if cerr := gw.Close(); cerr != nil && walkErr == nil {
		walkErr = cerr
	}
	if walkErr != nil {
		return "", 0, 0, fmt.Errorf("archive remote source: %w", walkErr)
	}
	if _, serr := f.Seek(0, io.SeekStart); serr != nil {
		return "", 0, 0, fmt.Errorf("seek temp archive: %w", serr)
	}
	data, rerr := io.ReadAll(f)
	if rerr != nil {
		return "", 0, 0, fmt.Errorf("read temp archive: %w", rerr)
	}
	if int64(len(data)) > archive.MaxCompressedSize {
		return "", 0, 0, fmt.Errorf("compressed archive size %d exceeds limit %d", len(data), archive.MaxCompressedSize)
	}
	return base64.StdEncoding.EncodeToString(data), int64(len(data)), n, nil
}

// handleDownload is the composite download callable: auto-detects file vs
// directory and returns base64 content. Files are capped at maxDownloadBytes;
// directories are archived into tar.gz (capped at archive.MaxCompressedSize).
func (a *Actor) handleDownload(ctx actor.PureContext, req domain.SshDownloadReq) (domain.SshDownloadResp, error) {
	fs, err := a.getFilesystem(ctx, req.SessionID)
	if err != nil {
		return domain.SshDownloadResp{}, err
	}
	defer fs.Close()

	name, content, size, isDir, numEntries, err := downloadFromFS(fs, req.Path)
	if err != nil {
		return domain.SshDownloadResp{}, err
	}
	return domain.SshDownloadResp{
		Name:        name,
		Content:     content,
		Size:        size,
		IsDirectory: isDir,
		NumEntries:  int32(numEntries),
	}, nil
}

// handleUpload is the composite upload callable: writes base64 content to a
// remote path as either a raw file (isArchive=false) or extracts a tar.gz
// archive into a directory (isArchive=true). Parent/target directories are
// auto-created. Registered with EffectIrreversible.
func (a *Actor) handleUpload(ctx actor.PureContext, req domain.SshUploadReq) (domain.SshUploadResp, error) {
	fs, err := a.getFilesystem(ctx, req.SessionID)
	if err != nil {
		return domain.SshUploadResp{}, err
	}
	defer fs.Close()

	numEntries, bytesWritten, err := uploadToFS(fs, req.Path, req.Content, req.IsArchive)
	if err != nil {
		return domain.SshUploadResp{}, err
	}
	return domain.SshUploadResp{
		NumEntries:   int32(numEntries),
		BytesWritten: bytesWritten,
	}, nil
}