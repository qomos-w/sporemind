package browserinstance

import (
	"context"
	"log/slog"
	"time"

	"github.com/qomos-w/sporemind/pkg/domain"
)

const operatorTimeout = 15 * time.Second

// timeoutOperator wraps a WindowOperator so each call runs in a goroutine with
// a bounded deadline. If the underlying Wails operation hangs, the caller gets
// an error instead of blocking the actor mailbox forever.
type timeoutOperator struct {
	inner WindowOperator
}

// NewTimeoutOperator wraps op with deadline protection.
func NewTimeoutOperator(op WindowOperator) WindowOperator {
	return &timeoutOperator{inner: op}
}

func (t *timeoutOperator) Create(id string, cfg domain.BrowserInstanceConfig) error {
	return t.runWithTimeout("create", id, func() error {
		return t.inner.Create(id, cfg)
	})
}

func (t *timeoutOperator) Close(id string) error {
	return t.runWithTimeout("close", id, func() error {
		return t.inner.Close(id)
	})
}

func (t *timeoutOperator) Navigate(id, url string) error {
	return t.runWithTimeout("navigate", id, func() error {
		return t.inner.Navigate(id, url)
	})
}

func (t *timeoutOperator) UpdateConfig(id string, cfg domain.BrowserInstanceConfig) error {
	return t.runWithTimeout("update_config", id, func() error {
		return t.inner.UpdateConfig(id, cfg)
	})
}

func (t *timeoutOperator) DeleteProfile(id string) error {
	return t.runWithTimeout("delete_profile", id, func() error {
		return t.inner.DeleteProfile(id)
	})
}

func (t *timeoutOperator) Observe(id string) (*domain.BrowserPageObservation, error) {
	var obs *domain.BrowserPageObservation
	err := t.runWithTimeout("observe", id, func() error {
		var observeErr error
		obs, observeErr = t.inner.Observe(id)
		return observeErr
	})
	return obs, err
}

func (t *timeoutOperator) Use(id string, req domain.BrowserUseReq) (*domain.BrowserUseResp, error) {
	var resp *domain.BrowserUseResp
	err := t.runWithTimeout("use", id, func() error {
		var useErr error
		resp, useErr = t.inner.Use(id, req)
		return useErr
	})
	return resp, err
}

func (t *timeoutOperator) ExportCookies(id string) (map[string][]domain.BrowserCookieEntry, error) {
	var result map[string][]domain.BrowserCookieEntry
	err := t.runWithTimeout("export_cookies", id, func() error {
		var exportErr error
		result, exportErr = t.inner.ExportCookies(id)
		return exportErr
	})
	return result, err
}

func (t *timeoutOperator) ImportCookies(id string, cookies map[string][]domain.BrowserCookieEntry) (int64, error) {
	var result int64
	err := t.runWithTimeout("import_cookies", id, func() error {
		var importErr error
		result, importErr = t.inner.ImportCookies(id, cookies)
		return importErr
	})
	return result, err
}

func (t *timeoutOperator) RegisterExternal(id string, win ExternalWindow) error {
	return t.runWithTimeout("register_external", id, func() error {
		return t.inner.RegisterExternal(id, win)
	})
}

func (t *timeoutOperator) runWithTimeout(op, id string, fn func() error) error {
	errCh := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				errCh <- errFromPanic(r)
			}
		}()
		errCh <- fn()
	}()

	select {
	case err := <-errCh:
		return err
	case <-time.After(operatorTimeout):
		slog.Error("browserinstance: operator timeout", "op", op, "id", id, "timeout", operatorTimeout)
		return context.DeadlineExceeded
	}
}

func errFromPanic(r any) error {
	return &panicError{v: r}
}

type panicError struct{ v any }

func (e *panicError) Error() string { return "browserinstance: operator panic recovered" }
