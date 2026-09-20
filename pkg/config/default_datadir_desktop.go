//go:build desktop

package config

import (
	"os"
	"path/filepath"
)

func defaultDataDir() string {
	if exe, err := os.Executable(); err == nil && exe != "" {
		return filepath.Join(filepath.Dir(exe), devReleaseDataDirName)
	}
	return devReleaseDataDirName
}
