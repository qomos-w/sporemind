// Package instanceid provides a stable per-installation fingerprint.
//
// The fingerprint identifies one sporemind instance across restarts so a
// remote-capable client can refuse to "connect" to itself. Resolution order:
//
//  1. SPOREMIND_INSTANCE_ID env override (tests, deterministic setups)
//  2. A persisted id file at <DataDir>/instance-id
//  3. A newly generated 16-byte random id (persisted to the file above)
package instanceid

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/qomos-w/sporemind/pkg/config"
)

const fileName = "instance-id"

var (
	once   sync.Once
	cached string
)

// Get returns the stable instance fingerprint for this installation. All
// callers within the same process receive the same value.
func Get() string {
	once.Do(func() {
		cached = loadOrGenerate()
	})
	return cached
}

func loadOrGenerate() string {
	if s := strings.TrimSpace(os.Getenv("SPOREMIND_INSTANCE_ID")); s != "" {
		return s
	}
	return resolveAt(config.DataDir())
}

// resolveAt returns the persisted id under dir, generating and persisting a
// fresh one on first use.
func resolveAt(dir string) string {
	if id := readPersistedAt(dir); id != "" {
		return id
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		// A non-persisted random id still identifies this process run; two
		// processes on the same machine would just never look "the same".
		return hex.EncodeToString(raw)
	}
	id := hex.EncodeToString(raw)
	if err := writePersistedAt(dir, id); err != nil {
		log.Printf("WARNING: instanceid: could not persist instance id: %v", err)
	}
	return id
}

func readPersistedAt(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(data))
	if len(s) == 32 {
		return s
	}
	return ""
}

func writePersistedAt(dir, id string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, fileName), []byte(id), 0600)
}
