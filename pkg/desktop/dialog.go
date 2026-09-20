package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/qomos-w/sporemind/pkg/appbinding"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// fileDialogOptionsFromReq maps the dialog.openFile wire request onto the
// wails file picker options. Pure function so the mapping is unit-testable
// without a running wails app (project rule: extract pure fragments from heavy
// assembly paths and test them directly).
func fileDialogOptionsFromReq(req appbinding.DialogOpenFileReq) application.OpenFileDialogOptions {
	opts := application.OpenFileDialogOptions{
		Title:                   req.Title,
		AllowsMultipleSelection: req.Multiple,
		CanChooseFiles:          true,
		CanChooseDirectories:    false,
	}
	for _, f := range req.Filters {
		if f.DisplayName == "" || f.Pattern == "" {
			continue
		}
		opts.Filters = append(opts.Filters, application.FileFilter{
			DisplayName: f.DisplayName,
			Pattern:     f.Pattern,
		})
	}
	return opts
}

// folderDialogOptionsFromReq maps the dialog.openFolder wire request onto the
// wails folder picker options. Folder picks are always single (Windows
// IFileDialog limitation), so AllowsMultipleSelection is false.
func folderDialogOptionsFromReq(req appbinding.DialogOpenFolderReq) application.OpenFileDialogOptions {
	return application.OpenFileDialogOptions{
		Title:                   req.Title,
		AllowsMultipleSelection: false,
		CanChooseFiles:          false,
		CanChooseDirectories:    true,
	}
}

// isDialogCancelled maps the wails cancel error onto the SDK wire contract.
// The Windows IFileDialog surfaces user cancellation as an error
// (cfd.ErrorCancelled, "cancelled by user") from PromptForSingleSelection;
// the dialog.openFile/openFolder/saveFile contract defines cancellation as a
// normal outcome ("empty array/empty path = cancelled"), so the seams
// downgrade it instead of failing the reverse call. The cfd package is
// wails-internal and cannot be imported, so the sentinel is matched by
// message.
func isDialogCancelled(err error) bool {
	return err != nil && strings.Contains(err.Error(), "cancelled by user")
}

// saveDialogOptionsFromReq maps the dialog.saveFile wire request onto the
// wails save dialog options. Pure function so the mapping is unit-testable
// without a running wails app. DefaultPath may be a directory, a full file
// path, or empty: when it carries a filename it is split into
// Directory + Filename so the dialog opens on the right folder with the name
// pre-filled.
func saveDialogOptionsFromReq(req appbinding.DialogSaveFileReq) application.SaveFileDialogOptions {
	opts := application.SaveFileDialogOptions{
		Title:                req.Title,
		CanCreateDirectories: true,
	}
	if req.DefaultPath != "" {
		if fi, err := os.Stat(req.DefaultPath); err == nil && fi.IsDir() {
			opts.Directory = req.DefaultPath
		} else {
			opts.Directory = filepath.Dir(req.DefaultPath)
			opts.Filename = filepath.Base(req.DefaultPath)
		}
	}
	for _, f := range req.Filters {
		if f.DisplayName == "" || f.Pattern == "" {
			continue
		}
		opts.Filters = append(opts.Filters, application.FileFilter{
			DisplayName: f.DisplayName,
			Pattern:     f.Pattern,
		})
	}
	return opts
}

// saveFileDialogPicker implements the appbinding.DesktopFileSavePicker seam:
// it pops the native save-file dialog per the wire request and returns the
// chosen absolute path (empty = cancelled). Assigned in ServiceStartup so the
// pluginhost's dialog.saveFile local host call reaches the desktop shell
// without any actor→desktop import.
func (a *App) saveFileDialogPicker(req appbinding.DialogSaveFileReq) (appbinding.DialogSaveResp, error) {
	if a.app == nil {
		return appbinding.DialogSaveResp{}, fmt.Errorf("desktop: not started")
	}
	opts := saveDialogOptionsFromReq(req)
	dlg := a.app.Dialog.SaveFileWithOptions(&opts)
	path, err := dlg.PromptForSingleSelection()
	if err != nil {
		if isDialogCancelled(err) {
			return appbinding.DialogSaveResp{Path: ""}, nil
		}
		return appbinding.DialogSaveResp{}, fmt.Errorf("desktop: dialog.saveFile: %w", err)
	}
	return appbinding.DialogSaveResp{Path: path}, nil
}

// openFileDialogPicker implements the appbinding.DesktopFilePicker seam: it
// pops the native open-file dialog per the wire request and returns the chosen
// absolute paths (empty array = cancelled). Assigned in ServiceStartup so the
// pluginhost's dialog.openFile local host call reaches the desktop shell
// without any actor→desktop import.
func (a *App) openFileDialogPicker(req appbinding.DialogOpenFileReq) (appbinding.DialogOpenResp, error) {
	if a.app == nil {
		return appbinding.DialogOpenResp{}, fmt.Errorf("desktop: not started")
	}
	opts := fileDialogOptionsFromReq(req)
	dlg := a.app.Dialog.OpenFileWithOptions(&opts)
	if opts.AllowsMultipleSelection {
		paths, err := dlg.PromptForMultipleSelection()
		if err != nil {
			if isDialogCancelled(err) {
				return appbinding.DialogOpenResp{Paths: []string{}}, nil
			}
			return appbinding.DialogOpenResp{}, fmt.Errorf("desktop: dialog.openFile: %w", err)
		}
		return appbinding.DialogOpenResp{Paths: paths}, nil
	}
	path, err := dlg.PromptForSingleSelection()
	if err != nil {
		if isDialogCancelled(err) {
			return appbinding.DialogOpenResp{Paths: []string{}}, nil
		}
		return appbinding.DialogOpenResp{}, fmt.Errorf("desktop: dialog.openFile: %w", err)
	}
	if path == "" {
		return appbinding.DialogOpenResp{Paths: []string{}}, nil
	}
	return appbinding.DialogOpenResp{Paths: []string{path}}, nil
}

// openFolderDialogPicker implements the appbinding.DesktopFolderPicker seam:
// it pops the native folder dialog and returns the single chosen absolute path
// (empty array = cancelled).
func (a *App) openFolderDialogPicker(req appbinding.DialogOpenFolderReq) (appbinding.DialogOpenResp, error) {
	if a.app == nil {
		return appbinding.DialogOpenResp{}, fmt.Errorf("desktop: not started")
	}
	opts := folderDialogOptionsFromReq(req)
	dlg := a.app.Dialog.OpenFileWithOptions(&opts)
	path, err := dlg.PromptForSingleSelection()
	if err != nil {
		if isDialogCancelled(err) {
			return appbinding.DialogOpenResp{Paths: []string{}}, nil
		}
		return appbinding.DialogOpenResp{}, fmt.Errorf("desktop: dialog.openFolder: %w", err)
	}
	if path == "" {
		return appbinding.DialogOpenResp{Paths: []string{}}, nil
	}
	return appbinding.DialogOpenResp{Paths: []string{path}}, nil
}
