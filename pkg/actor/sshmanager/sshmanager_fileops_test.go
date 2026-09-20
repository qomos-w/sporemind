package sshmanager

import (
	"encoding/base64"
	"fmt"
	"testing"
)

// TestDownloadSizeLimit verifies the download size guard rejects files larger
// than the configured limit while accepting files within it.
func TestDownloadSizeLimit(t *testing.T) {
	tests := []struct {
		name    string
		size    int64
		wantErr bool
	}{
		{"empty file", 0, false},
		{"small file", 1024, false},
		{"exactly at limit", maxDownloadBytes, false},
		{"one byte over limit", maxDownloadBytes + 1, true},
		{"huge file", 500 * 1024 * 1024, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkDownloadSize(tt.size)
			if (err != nil) != tt.wantErr {
				t.Fatalf("checkDownloadSize(%d) err=%v wantErr=%v", tt.size, err, tt.wantErr)
			}
		})
	}
}

// checkDownloadSize mirrors the size guard in handleFileDownload so it can be
// unit-tested without an SFTP session.
func checkDownloadSize(size int64) error {
	if size > maxDownloadBytes {
		return fmt.Errorf("file is %d bytes; download limit is %d bytes", size, maxDownloadBytes)
	}
	return nil
}

// TestDownloadEncodingRoundTrip verifies base64 encoding of file content
// survives a round-trip, matching what handleFileDownload produces and what
// the frontend decodes.
func TestDownloadEncodingRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"ascii text", []byte("hello world\n")},
		{"binary with nulls", []byte{0x00, 0x01, 0xFF, 0xFE, 0x00, 0x42}},
		{"utf-8", []byte("你好世界 🌍")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := base64.StdEncoding.EncodeToString(tt.data)
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				t.Fatalf("decode failed: %v", err)
			}
			if string(decoded) != string(tt.data) {
				t.Fatalf("round-trip mismatch: got %q want %q", decoded, tt.data)
			}
		})
	}
}

// TestDecodeBase64FileContent verifies the write_base64 payload decoder accepts
// everything the frontend encoder can produce (including binary bytes) and
// rejects malformed input, mirroring what handleFileWriteBase64 enforces before
// opening a remote session.
func TestDecodeBase64FileContent(t *testing.T) {
	t.Run("accepts valid payloads", func(t *testing.T) {
		payloads := []struct {
			name string
			data []byte
		}{
			{"empty file", []byte{}},
			{"ascii text", []byte("hello world\n")},
			{"binary with nulls", []byte{0x00, 0x01, 0xFF, 0xFE, 0x00, 0x42}},
			{"utf-8", []byte("你好世界 🌍")},
		}
		for _, tt := range payloads {
			t.Run(tt.name, func(t *testing.T) {
				decoded, err := decodeBase64FileContent(base64.StdEncoding.EncodeToString(tt.data))
				if err != nil {
					t.Fatalf("decodeBase64FileContent(%q) err=%v", tt.data, err)
				}
				if string(decoded) != string(tt.data) {
					t.Fatalf("round-trip mismatch: got %q want %q", decoded, tt.data)
				}
			})
		}
	})
	t.Run("rejects invalid base64", func(t *testing.T) {
		for _, bad := range []string{"not base64 !!", "aGVsbG8", "====", "abc def"} {
			if _, err := decodeBase64FileContent(bad); err == nil {
				t.Fatalf("decodeBase64FileContent(%q) expected error, got nil", bad)
			}
		}
	})
}

// TestParseOctalFileMode verifies the chmod mode validator accepts 3-digit
// octal strings and rejects everything else, matching what handleFileChmod
// enforces before opening a remote session.
func TestParseOctalFileMode(t *testing.T) {
	t.Run("accepts valid modes", func(t *testing.T) {
		for _, tt := range []struct {
			in   string
			want uint32
		}{
			{"755", 0o755},
			{"644", 0o644},
			{"064", 0o064},
			{"000", 0o000},
			{"777", 0o777},
		} {
			mode, err := parseOctalFileMode(tt.in)
			if err != nil {
				t.Fatalf("parseOctalFileMode(%q) err=%v", tt.in, err)
			}
			if uint32(mode.Perm()) != tt.want {
				t.Fatalf("parseOctalFileMode(%q) = %o want %o", tt.in, mode.Perm(), tt.want)
			}
		}
	})
	t.Run("rejects invalid modes", func(t *testing.T) {
		for _, bad := range []string{"", "75", "7555", "888", "abc", "7 5", "-1", "0o755"} {
			if _, err := parseOctalFileMode(bad); err == nil {
				t.Fatalf("parseOctalFileMode(%q) expected error, got nil", bad)
			}
		}
	})
}

// orderForDeletion reverses a walk-order path list so that children come before
// parents — the order removeDirRecursive uses to safely delete a directory tree.
func orderForDeletion(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[len(paths)-1-i] = p
	}
	return out
}

// TestOrderForDeletion verifies that the deletion ordering ensures every child
// path appears before its parent, which is required for RemoveDirectory to
// succeed on a non-empty SFTP directory.
func TestOrderForDeletion(t *testing.T) {
	// Simulate an sftp.Walk output: root is visited before its children.
	walkOrder := []string{
		"/site",
		"/site/index.html",
		"/site/assets",
		"/site/assets/style.css",
		"/site/assets/img",
		"/site/assets/img/logo.png",
	}
	deletionOrder := orderForDeletion(walkOrder)

	// Every parent must appear AFTER all its children in the deletion list.
	indexOf := func(p string) int {
		for i, path := range deletionOrder {
			if path == p {
				return i
			}
		}
		return -1
	}

	parentChild := []struct{ parent, child string }{
		{"/site", "/site/index.html"},
		{"/site", "/site/assets"},
		{"/site/assets", "/site/assets/style.css"},
		{"/site/assets", "/site/assets/img"},
		{"/site/assets/img", "/site/assets/img/logo.png"},
	}
	for _, pc := range parentChild {
		pi, ci := indexOf(pc.parent), indexOf(pc.child)
		if pi < 0 || ci < 0 {
			t.Fatalf("missing path in deletion order: parent=%q child=%q", pc.parent, pc.child)
		}
		if pi < ci {
			t.Fatalf("parent %q (index %d) deleted before child %q (index %d)", pc.parent, pi, pc.child, ci)
		}
	}
}
