package version

import (
	"os"
	"strings"
)

// Version is the sporemind version. It is overwritten by the build pipeline
// using -ldflags -X github.com/qomos-w/sporemind/pkg/version.Version=<version>.
var Version = readFallback()

func readFallback() string {
	if v := os.Getenv("SPOREMIND_VERSION"); v != "" {
		return strings.TrimSpace(v)
	}
	data, err := os.ReadFile("version")
	if err == nil && len(data) > 0 {
		return strings.TrimSpace(string(data))
	}
	return "dev"
}
