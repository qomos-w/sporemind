//go:build !desktop

package config

func defaultDataDir() string { return devReleaseDataDirName }
