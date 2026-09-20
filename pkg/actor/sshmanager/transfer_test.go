package sshmanager

import (
	"encoding/base64"
	"fmt"
	"path"
	"testing"
)

func TestDownloadFromFS_SingleFile(t *testing.T) {
	fs := newMockRemoteFS()
	fs.addFile("/remote/hello.txt", []byte("hello world\n"))

	name, content, size, isDir, n, err := downloadFromFS(fs, "/remote/hello.txt")
	if err != nil {
		t.Fatalf("downloadFromFS file: %v", err)
	}
	if name != "hello.txt" {
		t.Errorf("name = %q, want hello.txt", name)
	}
	if isDir {
		t.Error("isDir should be false for a file")
	}
	if n != 0 {
		t.Errorf("numEntries = %d, want 0 for single file", n)
	}
	data, derr := base64.StdEncoding.DecodeString(content)
	if derr != nil {
		t.Fatalf("base64 decode: %v", derr)
	}
	if string(data) != "hello world\n" {
		t.Errorf("content mismatch: %q", data)
	}
	if size != int64(len(data)) {
		t.Errorf("size = %d, want %d", size, len(data))
	}
}

func TestDownloadFromFS_Directory(t *testing.T) {
	fs := newMockRemoteFS()
	fs.seedTree("/site")

	name, content, size, isDir, n, err := downloadFromFS(fs, "/site")
	if err != nil {
		t.Fatalf("downloadFromFS dir: %v", err)
	}
	if name != "site" {
		t.Errorf("name = %q, want site", name)
	}
	if !isDir {
		t.Error("isDir should be true for a directory")
	}
	if n == 0 {
		t.Error("numEntries should be > 0 for a directory with files")
	}
	if content == "" {
		t.Error("content should not be empty")
	}
	if size == 0 {
		t.Error("size should be > 0 for an archive")
	}
	data, derr := base64.StdEncoding.DecodeString(content)
	if derr != nil {
		t.Fatalf("base64 decode: %v", derr)
	}
	if int64(len(data)) != size {
		t.Errorf("decoded size %d != reported size %d", len(data), size)
	}
}

func TestDownloadFromFS_EmptyPath(t *testing.T) {
	fs := newMockRemoteFS()
	_, _, _, _, _, err := downloadFromFS(fs, "")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestDownloadFromFS_NotFound(t *testing.T) {
	fs := newMockRemoteFS()
	_, _, _, _, _, err := downloadFromFS(fs, "/nonexistent")
	if err == nil {
		t.Fatal("expected error for non-existent path")
	}
}

func TestDownloadFromFS_SymlinkRejected(t *testing.T) {
	fs := newMockRemoteFS()
	fs.symlinks["/link"] = true
	_, _, _, _, _, err := downloadFromFS(fs, "/link")
	if err == nil {
		t.Fatal("symlink should be rejected")
	}
}

func TestUploadToFS_RawFile(t *testing.T) {
	fs := newMockRemoteFS()
	fs.dirs["/remote"] = true
	b64 := base64.StdEncoding.EncodeToString([]byte("uploaded content\n"))

	n, bw, err := uploadToFS(fs, "/remote/test.txt", b64, false)
	if err != nil {
		t.Fatalf("uploadToFS raw: %v", err)
	}
	if n != 0 {
		t.Errorf("numEntries = %d, want 0 for raw upload", n)
	}
	if bw != int64(len("uploaded content\n")) {
		t.Errorf("bytesWritten = %d, want %d", bw, len("uploaded content\n"))
	}
	got, ok := fs.files["/remote/test.txt"]
	if !ok {
		t.Fatal("file was not written")
	}
	if string(got) != "uploaded content\n" {
		t.Errorf("content = %q, want %q", got, "uploaded content\n")
	}
}

func TestUploadToFS_RawFile_AutoMkdir(t *testing.T) {
	fs := newMockRemoteFS()
	b64 := base64.StdEncoding.EncodeToString([]byte("data"))

	_, _, err := uploadToFS(fs, "/new/dir/file.txt", b64, false)
	if err != nil {
		t.Fatalf("uploadToFS auto-mkdir: %v", err)
	}
	if !fs.dirs["/new/dir"] {
		t.Error("parent directory was not auto-created")
	}
	got, ok := fs.files["/new/dir/file.txt"]
	if !ok {
		t.Fatal("file was not written")
	}
	if string(got) != "data" {
		t.Errorf("content = %q, want %q", got, "data")
	}
}

func TestUploadToFS_ArchiveExtract(t *testing.T) {
	srcFS := newMockRemoteFS()
	srcFS.seedTree("/src")
	_, b64, _, _, _, err := downloadFromFS(srcFS, "/src")
	if err != nil {
		t.Fatalf("export source: %v", err)
	}

	dstFS := newMockRemoteFS()
	n, bw, err := uploadToFS(dstFS, "/dst", b64, true)
	if err != nil {
		t.Fatalf("uploadToFS archive: %v", err)
	}
	if n == 0 {
		t.Error("numEntries should be > 0 for archive extract")
	}
	if bw <= 0 {
		t.Error("bytesWritten should be > 0")
	}
	if !dstFS.dirs["/dst"] {
		t.Error("target directory was not created")
	}
	got, ok := dstFS.files["/dst/src/readme.txt"]
	if !ok || string(got) != "hello world\n" {
		t.Errorf("readme.txt content = %q, want %q", got, "hello world\n")
	}
}

func TestUploadToFS_ArchiveExtract_AutoMkdir(t *testing.T) {
	srcFS := newMockRemoteFS()
	srcFS.seedTree("/src")
	_, b64, _, _, _, err := downloadFromFS(srcFS, "/src")
	if err != nil {
		t.Fatalf("export source: %v", err)
	}

	dstFS := newMockRemoteFS()
	_, _, err = uploadToFS(dstFS, "/nonexistent/deep/path", b64, true)
	if err != nil {
		t.Fatalf("uploadToFS archive auto-mkdir: %v", err)
	}
	if !dstFS.dirs["/nonexistent/deep/path"] {
		t.Error("target directory was not auto-created")
	}
}

func TestUploadToFS_InvalidBase64(t *testing.T) {
	fs := newMockRemoteFS()
	_, _, err := uploadToFS(fs, "/file.txt", "!!!not-base64!!!", false)
	if err == nil {
		t.Fatal("expected error for invalid base64")
	}
}

func TestUploadToFS_EmptyPath(t *testing.T) {
	fs := newMockRemoteFS()
	_, _, err := uploadToFS(fs, "", "", false)
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestUploadToFS_ArchiveTargetIsFile(t *testing.T) {
	fs := newMockRemoteFS()
	fs.addFile("/existing-file", []byte("data"))
	b64 := base64.StdEncoding.EncodeToString([]byte("archive"))

	_, _, err := uploadToFS(fs, "/existing-file", b64, true)
	if err == nil {
		t.Fatal("expected error when archive target is a file")
	}
}

func TestDownloadUploadRoundTrip(t *testing.T) {
	srcFS := newMockRemoteFS()
	srcFS.seedTree("/project")
	_, b64, _, _, _, err := downloadFromFS(srcFS, "/project")
	if err != nil {
		t.Fatalf("download: %v", err)
	}

	dstFS := newMockRemoteFS()
	n, bw, err := uploadToFS(dstFS, "/dest", b64, true)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if n == 0 {
		t.Fatal("no entries extracted")
	}
	if bw <= 0 {
		t.Fatal("no bytes written")
	}

	for _, check := range []struct {
		path string
		want string
	}{
		{path: "/dest/project/readme.txt", want: "hello world\n"},
		{path: "/dest/project/sub/a.txt", want: "aaa\n"},
	} {
		got, ok := dstFS.files[check.path]
		if !ok {
			t.Errorf("missing %q", check.path)
			continue
		}
		if string(got) != check.want {
			t.Errorf("%q = %q, want %q", check.path, got, check.want)
		}
	}

	for _, dir := range []string{"/dest/project", "/dest/project/sub", "/dest/project/sub/deep", "/dest/project/empty"} {
		if !dstFS.dirs[dir] {
			t.Errorf("directory %q not created", dir)
		}
	}
}

func TestDownloadFromFS_BinaryFile(t *testing.T) {
	fs := newMockRemoteFS()
	binaryData := []byte{0x00, 0x01, 0xFF, 0xFE, 0x42}
	fs.addFile("/remote/data.bin", binaryData)

	_, content, size, isDir, _, err := downloadFromFS(fs, "/remote/data.bin")
	if err != nil {
		t.Fatalf("downloadFromFS binary: %v", err)
	}
	if isDir {
		t.Error("isDir should be false")
	}
	data, derr := base64.StdEncoding.DecodeString(content)
	if derr != nil {
		t.Fatalf("base64 decode: %v", derr)
	}
	if fmt.Sprintf("%x", data) != fmt.Sprintf("%x", binaryData) {
		t.Errorf("binary content mismatch: %x vs %x", data, binaryData)
	}
	if size != int64(len(binaryData)) {
		t.Errorf("size = %d, want %d", size, len(binaryData))
	}
}

func TestUploadToFS_RawFile_RootPath(t *testing.T) {
	fs := newMockRemoteFS()
	b64 := base64.StdEncoding.EncodeToString([]byte("root file"))

	_, _, err := uploadToFS(fs, "/root-file.txt", b64, false)
	if err != nil {
		t.Fatalf("uploadToFS root path: %v", err)
	}
	got, ok := fs.files["/root-file.txt"]
	if !ok {
		t.Fatal("file was not written")
	}
	if string(got) != "root file" {
		t.Errorf("content = %q, want %q", got, "root file")
	}
}

func TestDownloadFromFS_DotPath(t *testing.T) {
	fs := newMockRemoteFS()
	_, _, _, _, _, err := downloadFromFS(fs, ".")
	if err == nil {
		t.Fatal("dot path should be rejected")
	}
}

func TestUploadToFS_RawFile_ParentExists(t *testing.T) {
	fs := newMockRemoteFS()
	fs.dirs["/existing"] = true
	b64 := base64.StdEncoding.EncodeToString([]byte("data"))

	_, _, err := uploadToFS(fs, path.Join("/existing", "file.txt"), b64, false)
	if err != nil {
		t.Fatalf("uploadToFS parent exists: %v", err)
	}
	got, ok := fs.files["/existing/file.txt"]
	if !ok || string(got) != "data" {
		t.Errorf("content = %q, want %q", got, "data")
	}
}