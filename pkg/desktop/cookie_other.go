//go:build !windows

package desktop

import (
	"errors"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func exportCookiesForWindow(_ *application.WebviewWindow) (map[string][]domain.BrowserCookieEntry, error) {
	return nil, errors.New("desktop: cookie export is only supported on Windows")
}

func importCookiesForWindow(_ *application.WebviewWindow, _ map[string][]domain.BrowserCookieEntry) (int64, error) {
	return 0, errors.New("desktop: cookie import is only supported on Windows")
}
