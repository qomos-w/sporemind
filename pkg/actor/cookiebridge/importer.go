package cookiebridge

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/qomos-w/gospore/ref"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// invokeTimeout bounds each browsermanager invoke from the bridge goroutines.
const invokeTimeout = 60 * time.Second

// CookieImporter is the seam between the bridge and browsermanager. The
// interface exists so tests can drive the full HTTP + MCP flow without a
// live desktop window stack.
type CookieImporter interface {
	// ResolveInstance maps an instance id or name to the canonical id; an
	// empty idOrName resolves the sole independent instance.
	ResolveInstance(ctx context.Context, idOrName string) (string, error)
	// Import pushes domain-grouped cookies into the instance and returns the
	// number written.
	Import(ctx context.Context, instanceID string, cookies map[string][]gen.BrowserCookieEntry) (int64, error)
}

// browserCookiesCallerHeaders carry an explicit admin identity: internal
// actor→actor invokes travel with zero identity, and
// browsermanager.import_cookies is AdminOnly. This mirrors the pluginhost
// browser.cookies pattern; browsermanager keeps its AdminOnly semantics for
// every other caller.
var browserCookiesCallerHeaders = map[string]string{
	"gospore.caller_role":    "admin",
	"gospore.caller_subject": "sporemind-cookiebridge",
}

// browserImporter implements CookieImporter against the live browsermanager
// service ref. The ref is resolved once (OnStart) and read from many
// goroutines, hence the mutex.
type browserImporter struct {
	mu sync.Mutex
	bm ref.Ref
}

func (b *browserImporter) setRef(r ref.Ref) {
	b.mu.Lock()
	b.bm = r
	b.mu.Unlock()
}

func (b *browserImporter) service() (ref.Ref, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.bm == nil {
		return nil, fmt.Errorf("cookiebridge: browsermanager service not available")
	}
	return b.bm, nil
}

func (b *browserImporter) ResolveInstance(ctx context.Context, idOrName string) (string, error) {
	bm, err := b.service()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, invokeTimeout)
	defer cancel()
	call := bm.Invoke(ctx, "browsermanager.list", nil, browserCookiesCallerHeaders)
	if call == nil {
		return "", fmt.Errorf("cookiebridge: browsermanager.list invoke returned nil")
	}
	raw, err := call.RecvRaw()
	call.Close()
	if err != nil {
		return "", fmt.Errorf("cookiebridge: browsermanager.list: %w", err)
	}
	var list domain.BrowserManagerListResp
	if err := json.Unmarshal(raw, &list); err != nil {
		return "", fmt.Errorf("cookiebridge: decode browsermanager.list: %w", err)
	}
	if idOrName != "" {
		for _, inst := range list.Items {
			if inst.Config.ID == idOrName || inst.Config.Name == idOrName {
				return inst.Config.ID, nil
			}
		}
		return "", fmt.Errorf("cookiebridge: browser instance %q not found", idOrName)
	}
	if len(list.Items) == 1 {
		return list.Items[0].Config.ID, nil
	}
	return "", fmt.Errorf("cookiebridge: instanceId is required (%d independent instances exist)", len(list.Items))
}

func (b *browserImporter) Import(ctx context.Context, instanceID string, cookies map[string][]gen.BrowserCookieEntry) (int64, error) {
	bm, err := b.service()
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, invokeTimeout)
	defer cancel()
	payload, err := json.Marshal(gen.BrowserManagerImportCookiesReq{ID: instanceID, Cookies: cookies})
	if err != nil {
		return 0, fmt.Errorf("cookiebridge: encode import request: %w", err)
	}
	call := bm.Invoke(ctx, "browsermanager.import_cookies", payload, browserCookiesCallerHeaders)
	if call == nil {
		return 0, fmt.Errorf("cookiebridge: browsermanager.import_cookies invoke returned nil")
	}
	raw, err := call.RecvRaw()
	call.Close()
	if err != nil {
		return 0, fmt.Errorf("cookiebridge: browsermanager.import_cookies: %w", err)
	}
	var resp gen.BrowserManagerImportCookiesResp
	if err := json.Unmarshal(raw, &resp); err != nil {
		return 0, fmt.Errorf("cookiebridge: decode import response: %w", err)
	}
	return resp.Imported, nil
}
