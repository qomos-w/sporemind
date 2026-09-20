//go:build !windows

// Package winx is a no-op on non-Windows builds. The computeruse actor
// falls back to a clear "platform not supported" error.
package winx
