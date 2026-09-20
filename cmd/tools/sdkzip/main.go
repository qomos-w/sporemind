package main

// pkgsdkzip packs the plugin SDK source tree into a deterministic zip for
// embedding into release builds (pkg/codegen/sdk.zip). The archive mirrors the
// vendor-sdk copy rules: examples/ and dot-prefixed entries are excluded so
// the embedded SDK compiles standalone and stays small.
//
// Usage: go run ./cmd/tools/sdkzip -sdk sporemind-plugin-sdk -out pkg/codegen/sdk.zip
// Prints the SHA-256 hex of the written archive on stdout.

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	sdkDir := flag.String("sdk", "sporemind-plugin-sdk", "SDK source directory")
	out := flag.String("out", "pkg/codegen/sdk.zip", "output zip path")
	flag.Parse()

	if _, err := os.Stat(filepath.Join(*sdkDir, "go.mod")); err != nil {
		fmt.Fprintf(os.Stderr, "sdkzip: %s is not an SDK module (no go.mod): %v\n", *sdkDir, err)
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "sdkzip: mkdir: %v\n", err)
		os.Exit(1)
	}
	f, err := os.Create(*out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sdkzip: create: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	// Fixed timestamps keep the archive byte-identical for identical sources.
	const fixedTime = int64(1700000000)
	written := 0
	err = filepath.WalkDir(*sdkDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(*sdkDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		name := filepath.Base(rel)
		if name == "examples" && d.IsDir() {
			return filepath.SkipDir
		}
		if strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		hdr := &zip.FileHeader{Name: rel, Method: zip.Deflate}
		hdr.SetModTime(time.Unix(fixedTime, 0))
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
		written++
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "sdkzip: walk: %v\n", err)
		os.Exit(1)
	}
	if err := zw.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "sdkzip: close: %v\n", err)
		os.Exit(1)
	}
	if err := f.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "sdkzip: close file: %v\n", err)
		os.Exit(1)
	}

	data, err := os.ReadFile(*out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sdkzip: re-read: %v\n", err)
		os.Exit(1)
	}
	sum := sha256.Sum256(data)
	fmt.Printf("%s %s %d files\n", hex.EncodeToString(sum[:]), *out, written)
}