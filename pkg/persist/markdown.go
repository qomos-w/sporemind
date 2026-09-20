package persist

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type Markdown struct {
	Raw string
}

type MarkdownPersist struct {
	BaseDir string
}

func NewMarkdownPersist(baseDir string) Persist {
	return &MarkdownPersist{BaseDir: baseDir}
}

func (s *MarkdownPersist) path(name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || filepath.IsAbs(clean) || strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("persist: invalid markdown name %q", name)
	}
	return filepath.Join(s.BaseDir, clean+".md"), nil
}

func (s *MarkdownPersist) Save(name string, v any) error {
	doc, ok := v.(Markdown)
	if !ok {
		return fmt.Errorf("persist: markdown save requires persist.Markdown")
	}
	path, err := s.path(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("persist: mkdir: %w", err)
	}
	return WriteFileAtomic(path, []byte(doc.Raw), 0644)
}

func (s *MarkdownPersist) Load(name string, v any) error {
	doc, ok := v.(*Markdown)
	if !ok {
		return fmt.Errorf("persist: markdown load requires *persist.Markdown")
	}
	path, err := s.path(name)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotExist
	}
	if err != nil {
		return fmt.Errorf("persist: read: %w", err)
	}
	doc.Raw = string(data)
	return nil
}

func (s *MarkdownPersist) Delete(name string) error {
	path, err := s.path(name)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("persist: delete: %w", err)
	}
	return nil
}

// List implements Lister: it walks the prefix subtree and returns the
// persisted names (relative to BaseDir, forward slashes, ".md" stripped).
// Transient atomic-write temp files ("<name>.md.tmp") and non-markdown
// files are skipped. A prefix that does not exist yields an empty list.
func (s *MarkdownPersist) List(prefix string) ([]string, error) {
	root := filepath.Join(s.BaseDir, filepath.Clean(filepath.FromSlash(prefix)))
	var names []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && path == root {
				return nil
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		rel, relErr := filepath.Rel(s.BaseDir, path)
		if relErr != nil {
			return relErr
		}
		names = append(names, strings.TrimSuffix(filepath.ToSlash(rel), ".md"))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("persist: list: %w", err)
	}
	return names, nil
}
