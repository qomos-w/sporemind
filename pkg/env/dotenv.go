package env

import (
	"bufio"
	"io"
	"os"
	"strings"
)

// LoadDotenv reads KEY=VALUE lines from a file and injects them into the
// process environment. Lines starting with # are ignored. Blank lines are
// skipped. If the file does not exist, LoadDotenv returns nil (no error).
// Variables already set in the environment are NOT overwritten — this lets
// explicit shell exports take precedence over the file.
func LoadDotenv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	return LoadDotenvReader(f)
}

func LoadDotenvReader(r io.Reader) error {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if key == "" {
			continue
		}
		val = unquote(val)
		if os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
	return scanner.Err()
}

func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
