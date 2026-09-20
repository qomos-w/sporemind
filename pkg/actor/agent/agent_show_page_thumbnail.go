package agent

import (
	"strings"

	"github.com/qomos-w/gospore/actor"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// handleShowPageThumbnail resolves the requested URL (optionally against the
// project root) and returns it so the frontend can render a thumbnail card in
// the ai-step timeline. The tool performs no side effect: the page is only
// opened (in the shared global browser tab) when the user clicks the card.
func (a *Actor) handleShowPageThumbnail(ctx actor.Context, req gen.AgentShowPageThumbnailReq) (gen.AgentShowPageThumbnailResp, error) {
	resolved, err := a.resolveGlobalBrowserURL(ctx, req.URL)
	if err != nil {
		return gen.AgentShowPageThumbnailResp{Error: err.Error()}, nil
	}
	if resolved == "" {
		return gen.AgentShowPageThumbnailResp{Error: "show_page_thumbnail: url is required"}, nil
	}
	resp := gen.AgentShowPageThumbnailResp{URL: resolved, Title: req.Title}
	if strings.HasPrefix(resolved, "file://") {
		resp.FilePath = fileURLToPath(resolved)
	}
	return resp, nil
}

// fileURLToPath converts a file:// URL to a filesystem path.
// file:///F:/path → F:/path ; file:///path → /path
func fileURLToPath(fileURL string) string {
	s := strings.TrimPrefix(fileURL, "file://")
	// On Windows, file:///F:/... has an extra leading slash before the drive letter.
	if len(s) > 2 && s[0] == '/' && s[2] == ':' {
		return s[1:]
	}
	return s
}
