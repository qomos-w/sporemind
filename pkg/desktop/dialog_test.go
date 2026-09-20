package desktop

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/qomos-w/sporemind/pkg/appbinding"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestFileDialogOptionsFromReq(t *testing.T) {
	cases := []struct {
		name  string
		req   appbinding.DialogOpenFileReq
		check func(t *testing.T, o dialogOptionsForTest)
	}{
		{
			name: "defaults choose files not directories",
			req:  appbinding.DialogOpenFileReq{},
			check: func(t *testing.T, o dialogOptionsForTest) {
				if !o.CanChooseFiles || o.CanChooseDirectories {
					t.Errorf("file/dir = %v/%v, want true/false", o.CanChooseFiles, o.CanChooseDirectories)
				}
				if o.AllowsMultipleSelection {
					t.Errorf("AllowsMultipleSelection = true, want false")
				}
			},
		},
		{
			name: "title and multiple carried",
			req:  appbinding.DialogOpenFileReq{Multiple: true, Title: "挑一本"},
			check: func(t *testing.T, o dialogOptionsForTest) {
				if o.Title != "挑一本" || !o.AllowsMultipleSelection {
					t.Errorf("title/multiple = %q/%v", o.Title, o.AllowsMultipleSelection)
				}
			},
		},
		{
			name: "filters mapped, incomplete skipped",
			req: appbinding.DialogOpenFileReq{Filters: []appbinding.DialogFileFilter{
				{DisplayName: "Text", Pattern: "*.txt;*.md"},
				{DisplayName: "Broken"}, // no pattern → skipped
			}},
			check: func(t *testing.T, o dialogOptionsForTest) {
				if len(o.Filters) != 1 || o.Filters[0].Pattern != "*.txt;*.md" {
					t.Errorf("filters = %+v, want one Text filter", o.Filters)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := fileDialogOptionsFromReq(tc.req)
			tc.check(t, dialogOptionsForTest{
				CanChooseFiles:          opts.CanChooseFiles,
				CanChooseDirectories:    opts.CanChooseDirectories,
				AllowsMultipleSelection: opts.AllowsMultipleSelection,
				Title:                   opts.Title,
				Filters:                 opts.Filters,
			})
		})
	}
}

// TestIsDialogCancelled pins the wire-contract downgrade: the wails cancel
// sentinel (message-matched; the cfd package is internal) must be recognized
// so the seams can answer "empty array = cancelled" instead of failing.
func TestIsDialogCancelled(t *testing.T) {
	if !isDialogCancelled(fmt.Errorf("desktop: dialog.openFolder: %w", errCancelled())) {
		t.Error("cancel sentinel must be recognized")
	}
	if isDialogCancelled(fmt.Errorf("no native picker")) {
		t.Error("unrelated errors must not be treated as cancel")
	}
	if isDialogCancelled(nil) {
		t.Error("nil error is not a cancel")
	}
}

// errCancelled mirrors the wails cfd.ErrorCancelled message without importing
// the internal package.
func errCancelled() error { return errors.New("cancelled by user") }

func TestFolderDialogOptionsFromReq(t *testing.T) {
	opts := folderDialogOptionsFromReq(appbinding.DialogOpenFolderReq{Title: "挑目录"})
	if opts.CanChooseFiles || !opts.CanChooseDirectories {
		t.Errorf("file/dir = %v/%v, want false/true", opts.CanChooseFiles, opts.CanChooseDirectories)
	}
	if opts.AllowsMultipleSelection {
		t.Errorf("AllowsMultipleSelection = true, want false (folder picks are always single)")
	}
	if opts.Title != "挑目录" {
		t.Errorf("title = %q, want 挑目录", opts.Title)
	}
}

// dialogOptionsForTest mirrors the wails OpenFileDialogOptions fields this
// mapping touches, so the test asserts a plain struct instead of importing
// the wails application package here.
type dialogOptionsForTest struct {
	CanChooseFiles          bool
	CanChooseDirectories    bool
	AllowsMultipleSelection bool
	Title                   string
	Filters                 []application.FileFilter
}

func TestSaveDialogOptionsFromReq(t *testing.T) {
	cases := []struct {
		name  string
		req   appbinding.DialogSaveFileReq
		check func(t *testing.T, o application.SaveFileDialogOptions)
	}{
		{
			name: "title carried, create directories on",
			req:  appbinding.DialogSaveFileReq{Title: "另存为"},
			check: func(t *testing.T, o application.SaveFileDialogOptions) {
				if o.Title != "另存为" {
					t.Errorf("title = %q, want 另存为", o.Title)
				}
				if !o.CanCreateDirectories {
					t.Error("CanCreateDirectories = false, want true")
				}
				if o.Directory != "" || o.Filename != "" {
					t.Errorf("directory/filename = %q/%q, want empty defaults", o.Directory, o.Filename)
				}
			},
		},
		{
			name: "existing directory seeds directory only",
			req:  appbinding.DialogSaveFileReq{DefaultPath: os.TempDir()},
			check: func(t *testing.T, o application.SaveFileDialogOptions) {
				if filepath.ToSlash(o.Directory) != filepath.ToSlash(os.TempDir()) {
					t.Errorf("directory = %q, want the requested existing directory", o.Directory)
				}
				if o.Filename != "" {
					t.Errorf("filename = %q, want empty", o.Filename)
				}
			},
		},
		{
			name: "full path split into directory + filename",
			req:  appbinding.DialogSaveFileReq{DefaultPath: filepath.Join(os.TempDir(), "sprite.aseprite")},
			check: func(t *testing.T, o application.SaveFileDialogOptions) {
				if filepath.ToSlash(o.Directory) != filepath.ToSlash(os.TempDir()) {
					t.Errorf("directory = %q, want the temp dir", o.Directory)
				}
				if o.Filename != "sprite.aseprite" {
					t.Errorf("filename = %q, want sprite.aseprite", o.Filename)
				}
			},
		},
		{
			name: "filters mapped, incomplete skipped",
			req: appbinding.DialogSaveFileReq{Filters: []appbinding.DialogFileFilter{
				{DisplayName: "Aseprite", Pattern: "*.aseprite"},
				{Pattern: "*.orphan"}, // no display name → skipped
			}},
			check: func(t *testing.T, o application.SaveFileDialogOptions) {
				if len(o.Filters) != 1 || o.Filters[0].Pattern != "*.aseprite" {
					t.Errorf("filters = %+v, want one Aseprite filter", o.Filters)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.check(t, saveDialogOptionsFromReq(tc.req))
		})
	}
}
