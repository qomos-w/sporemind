package appmanager

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

const previewManifestJSON = `{
	"id": "app.preview-demo",
	"name": "PreviewDemo",
	"version": "0.1.0",
	"runtime": "spore",
	"protocolVersion": 1,
	"namespace": "app.previewdemo",
	"permissions": ["shell.exec", "fs.read"]
}`

func TestRegistrationPreviewInlineManifest(t *testing.T) {
	var m gen.AppManifest
	if err := json.Unmarshal([]byte(previewManifestJSON), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	resp, err := (&Actor{}).handleRegistrationPreview(nil, gen.AppManagerRegistrationPreviewReq{Manifest: &m})
	if err != nil {
		t.Fatalf("handleRegistrationPreview: %v", err)
	}
	if resp.Manifest.ID != "app.preview-demo" {
		t.Fatalf("id = %q", resp.Manifest.ID)
	}
	if len(resp.Manifest.Permissions) != 2 || resp.Manifest.Permissions[0] != "shell.exec" {
		t.Fatalf("permissions = %v", resp.Manifest.Permissions)
	}
}

func TestRegistrationPreviewLocalDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.manifest.json"), []byte(previewManifestJSON), 0644); err != nil {
		t.Fatal(err)
	}
	resp, err := (&Actor{}).handleRegistrationPreview(nil, gen.AppManagerRegistrationPreviewReq{Path: dir})
	if err != nil {
		t.Fatalf("handleRegistrationPreview: %v", err)
	}
	if resp.Manifest.ID != "app.preview-demo" {
		t.Fatalf("id = %q", resp.Manifest.ID)
	}
}

func TestRegistrationPreviewLocalZip(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "pkg.zip")
	zf, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	w, err := zw.Create("app.manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(previewManifestJSON)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zf.Close()

	resp, err := (&Actor{}).handleRegistrationPreview(nil, gen.AppManagerRegistrationPreviewReq{Path: zipPath})
	if err != nil {
		t.Fatalf("handleRegistrationPreview: %v", err)
	}
	if resp.Manifest.ID != "app.preview-demo" {
		t.Fatalf("id = %q", resp.Manifest.ID)
	}
}

func TestRegistrationPreviewLocalDirMissingManifest(t *testing.T) {
	dir := t.TempDir()
	_, err := (&Actor{}).handleRegistrationPreview(nil, gen.AppManagerRegistrationPreviewReq{Path: dir})
	if err == nil {
		t.Fatalf("expected error for missing app.manifest.json")
	}
}

func TestRegistrationPreviewNoSource(t *testing.T) {
	_, err := (&Actor{}).handleRegistrationPreview(nil, gen.AppManagerRegistrationPreviewReq{})
	if err == nil {
		t.Fatalf("expected error when no source field is set")
	}
}
