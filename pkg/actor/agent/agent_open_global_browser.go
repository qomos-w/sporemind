package agent

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// handleOpenGlobalBrowser resolves the requested URL (optionally against the
// project root) and asks the browsermanager to open it in the shared global
// browser tab. The actual window creation is performed by the desktop frontend
// in response to the emitted browser_manager_event.
func (a *Actor) handleOpenGlobalBrowser(ctx actor.PureContext, req gen.AgentOpenGlobalBrowserReq) (gen.AgentOpenGlobalBrowserResp, error) {
	resolved, err := a.resolveGlobalBrowserURL(ctx, req.URL)
	if err != nil {
		return gen.AgentOpenGlobalBrowserResp{Opened: false, Error: err.Error()}, nil
	}
	if resolved == "" {
		return gen.AgentOpenGlobalBrowserResp{Opened: false, Error: "open_global_browser: url is required"}, nil
	}

	planner := ctx.Planner()
	if planner == nil {
		return gen.AgentOpenGlobalBrowserResp{Opened: false, Error: "open_global_browser: planner not available"}, nil
	}
	mgrRef, ok := ctx.LookupService("browsermanager")
	if !ok {
		return gen.AgentOpenGlobalBrowserResp{Opened: false, Error: "open_global_browser: browsermanager service not available"}, nil
	}

	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	result, err := planner.Call(callCtx, mgrRef, "browsermanager.open_global", domain.BrowserManagerOpenGlobalReq{URL: resolved}).Await()
	if err != nil {
		return gen.AgentOpenGlobalBrowserResp{Opened: false, Error: fmt.Sprintf("open_global_browser: %v", err)}, nil
	}
	resp, ok := result.(domain.BrowserManagerOpenGlobalResp)
	if !ok {
		return gen.AgentOpenGlobalBrowserResp{Opened: false, Error: "open_global_browser: unexpected response from browsermanager"}, nil
	}
	return gen.AgentOpenGlobalBrowserResp{Opened: resp.Opened, URL: resp.URL, Error: resp.Error}, nil
}

// resolveGlobalBrowserURL turns agent input into a loadable URL.
//   - http:// / https:// / file:// / about:// are kept as-is.
//   - Absolute filesystem paths become file:// URLs.
//   - Relative paths are resolved against the project root, then converted to file://.
//   - Bare host-like input (e.g. "example.com") is left untouched so the frontend
//     can normalize it.
func (a *Actor) resolveGlobalBrowserURL(ctx actor.PureContext, input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", nil
	}

	// Already a URL scheme we trust.
	if strings.HasPrefix(input, "http://") ||
		strings.HasPrefix(input, "https://") ||
		strings.HasPrefix(input, "file://") ||
		strings.HasPrefix(input, "about:") {
		return input, nil
	}

	// Windows drive-letter path -> file://
	if len(input) >= 3 && input[1] == ':' && (input[2] == '\\' || input[2] == '/') {
		abs := filepath.ToSlash(input)
		return "file:///" + abs, nil
	}

	// Unix absolute path -> file://
	if strings.HasPrefix(input, "/") {
		return "file://" + filepath.ToSlash(input), nil
	}

	// Relative path -> resolve against project root -> file://, but only if it
	// looks like a path (contains a separator). Host-like input such as
	// "example.com" is left as-is so the frontend can normalize it.
	if strings.ContainsAny(input, "/\\") {
		projectRoot := a.resolveProjectRootPure(ctx)
		if projectRoot != "" {
			abs := filepath.Join(projectRoot, input)
			clean := filepath.Clean(abs)
			// Safety check: resolved path must stay inside project root.
			if !isPathInside(projectRoot, clean) {
				return "", fmt.Errorf("open_global_browser: path %q escapes project root", input)
			}
			// Only convert to file:// if the path actually exists; otherwise it
			// is likely a malformed URL and we should not fabricate a file.
			if _, err := os.Stat(clean); err == nil {
				return "file:///" + filepath.ToSlash(clean), nil
			}
		}
	}

	// Contains a scheme we don't explicitly handle -> keep as-is.
	if strings.Contains(input, "://") {
		return input, nil
	}
	// Could be a bare host/keyword; leave it for the frontend to normalize.
	// Validate it doesn't contain dangerous characters.
	if _, err := url.Parse(input); err != nil {
		return "", fmt.Errorf("open_global_browser: invalid URL %q: %w", input, err)
	}
	return input, nil
}

// isPathInside reports whether child is equal to or inside parent, using clean
// absolute paths. It prevents relative paths from escaping the project root.
func isPathInside(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
