package lspserver

import (
	"encoding/hex"
	"strings"
	"testing"
)

// TestManifestSHA256Format verifies every pinned manifest checksum is a real,
// 64-character lowercase hex digest rather than the placeholder zero value.
func TestManifestSHA256Format(t *testing.T) {
	tests := []struct {
		name string
		sum  string
	}{
		{"pyright", pyrightSHA256},
		{"vscode-langservers-extracted", webLangserversSHA256},
		{"bash-language-server", bashLanguageServerSHA256},
		{"rust-analyzer linux amd64", rustAnalyzerLinuxAMD64SHA256},
		{"rust-analyzer linux arm64", rustAnalyzerLinuxARM64SHA256},
		{"rust-analyzer darwin amd64", rustAnalyzerDarwinAMD64SHA256},
		{"rust-analyzer darwin arm64", rustAnalyzerDarwinARM64SHA256},
		{"rust-analyzer windows amd64", rustAnalyzerWindowsAMD64SHA256},
		{"clangd linux amd64", clangdLinuxAMD64SHA256},
		{"clangd darwin", clangdDarwinSHA256},
		{"clangd windows amd64", clangdWindowsAMD64SHA256},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.sum) != 64 {
				t.Fatalf("sha256 %q has length %d, want 64 hex characters", tt.sum, len(tt.sum))
			}
			if _, err := hex.DecodeString(tt.sum); err != nil {
				t.Fatalf("sha256 %q is not valid hex: %v", tt.sum, err)
			}
			if tt.sum == strings.Repeat("0", 64) {
				t.Fatalf("sha256 %q is the placeholder zero digest", tt.sum)
			}
		})
	}
}
