package shell

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// VFS abstracts file system operations for sandboxed command execution.
type VFS interface {
	// Root returns the absolute sandbox root path.
	Root() string

	// ReadFile reads the entire file at the given path.
	ReadFile(path string) ([]byte, error)

	// WriteFile writes data to the given file with the specified permissions.
	WriteFile(path string, data []byte, perm uint32) error

	// ReadDir returns directory entries at the given path.
	ReadDir(path string) ([]fs.DirEntry, error)

	// MkdirAll creates a directory and any necessary parents.
	MkdirAll(path string, perm uint32) error

	// Remove deletes a file.
	Remove(path string) error

	// RemoveAll recursively deletes a file or directory.
	RemoveAll(path string) error

	// Stat returns file info for the given path.
	Stat(path string) (fs.FileInfo, error)
}

// RealVFS implements VFS backed by the host file system with a sandbox root.
// All paths are resolved relative to Root and cannot escape above it.
type RealVFS struct {
	root string
}

// NewRealVFS creates a VFS rooted at the given directory.
// Empty root defaults to the current working directory.
func NewRealVFS(root string) *RealVFS {
	if root == "" {
		root, _ = os.Getwd()
	}
	abs, _ := filepath.Abs(root)
	return &RealVFS{root: abs}
}

// Root returns the sandbox root.
func (v *RealVFS) Root() string { return v.root }

// resolve converts a VFS-relative path to an absolute host path.
// Returns error if the path escapes the sandbox.
func (v *RealVFS) resolve(p string) (string, error) {
	clean := filepath.Clean(filepath.Join(v.root, p))
	// Ensure the resolved path is inside the sandbox.
	if !strings.HasPrefix(clean+string(filepath.Separator), v.root+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes sandbox: %s", p)
	}
	return clean, nil
}

func (v *RealVFS) ReadFile(p string) ([]byte, error) {
	abs, err := v.resolve(p)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(abs)
}

func (v *RealVFS) WriteFile(p string, data []byte, perm uint32) error {
	abs, err := v.resolve(p)
	if err != nil {
		return err
	}
	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		return err
	}
	return os.WriteFile(abs, data, os.FileMode(perm))
}

func (v *RealVFS) ReadDir(p string) ([]fs.DirEntry, error) {
	abs, err := v.resolve(p)
	if err != nil {
		return nil, err
	}
	return os.ReadDir(abs)
}

func (v *RealVFS) MkdirAll(p string, perm uint32) error {
	abs, err := v.resolve(p)
	if err != nil {
		return err
	}
	return os.MkdirAll(abs, os.FileMode(perm))
}

func (v *RealVFS) Remove(p string) error {
	abs, err := v.resolve(p)
	if err != nil {
		return err
	}
	return os.Remove(abs)
}

func (v *RealVFS) RemoveAll(p string) error {
	abs, err := v.resolve(p)
	if err != nil {
		return err
	}
	return os.RemoveAll(abs)
}

func (v *RealVFS) Stat(p string) (fs.FileInfo, error) {
	abs, err := v.resolve(p)
	if err != nil {
		return nil, err
	}
	return os.Stat(abs)
}
