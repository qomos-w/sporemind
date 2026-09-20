package storeclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// indexCacheTTL mirrors the cloud's Cache-Control max-age=60 on /store/index.
const indexCacheTTL = 60 * time.Second

// storeIndexWire is the cloud /store/index JSON shape (snake_case wire).
type storeIndexWire struct {
	GeneratedAt string               `json:"generated_at"`
	Store       []storePluginWire    `json:"store"`
	Community   []storeCommunityWire `json:"community"`
}

type storePluginWire struct {
	ID             string             `json:"id"`
	Slug           string             `json:"slug"`
	DisplayName    string             `json:"display_name"`
	Description    string             `json:"description"`
	Publisher      string             `json:"publisher"`
	Channel        string             `json:"channel"`
	PublisherPubkey string            `json:"publisher_pubkey"`
	CurrentVersion string             `json:"current_version"`
	Versions       []storeVersionWire `json:"versions"`
}

type storeVersionWire struct {
	Version         string `json:"version"`
	SdkVersion      string `json:"sdk_version"`
	ProtocolVersion int32  `json:"protocol_version"`
	MinHostVersion  string `json:"min_host_version"`
	PayloadSha256   string `json:"payload_sha256"`
	CiphertextSha256 string `json:"ciphertext_sha256"`
	Signature       string `json:"signature"`
	Size            int64  `json:"size"`
	Changelog       string `json:"changelog"`
	CreatedAt       string `json:"created_at"`
}

type storeCommunityWire struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	RepoUrl     string `json:"repo_url"`
	CommitSha   string `json:"commit_sha"`
	Version     string `json:"version"`
	SdkVersion  string `json:"sdk_version"`
	ProtocolVersion int32 `json:"protocol_version"`
	TarballSha256 string `json:"tarball_sha256"`
	MirrorUrl    string `json:"mirror_url"`
	Manifest struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Permissions []string `json:"permissions"`
	} `json:"manifest"`
}

// cachedIndex is the TTL-cached index plus fetch metadata.
type cachedIndex struct {
	wire      storeIndexWire
	fetchedAt time.Time
}

// fetchIndex returns the store index, using the TTL cache when fresh. The
// fetch itself runs outside the actor lock (network IO on the store_net
// lane); only the cache swap is locked.
func (a *Actor) fetchIndex(ctx context.Context, force bool) (storeIndexWire, time.Time, bool, error) {
	a.mu.RLock()
	cached := a.index
	a.mu.RUnlock()
	if !force && !cached.fetchedAt.IsZero() && time.Since(cached.fetchedAt) < indexCacheTTL {
		return cached.wire, cached.fetchedAt, true, nil
	}

	baseURL, err := a.baseURL()
	if err != nil {
		return storeIndexWire{}, time.Time{}, false, err
	}
	body, err := httpGetAll(ctx, a.http, joinURL(baseURL, "/store/index"))
	if err != nil {
		// Serve a stale cache rather than failing when the cloud is
		// momentarily unreachable.
		if !cached.fetchedAt.IsZero() {
			return cached.wire, cached.fetchedAt, true, nil
		}
		return storeIndexWire{}, time.Time{}, false, fmt.Errorf("storeclient: fetch /store/index: %w", err)
	}
	var wire storeIndexWire
	if err := json.Unmarshal(body, &wire); err != nil {
		return storeIndexWire{}, time.Time{}, false, fmt.Errorf("storeclient: decode /store/index: %w", err)
	}
	now := time.Now()
	a.mu.Lock()
	a.index = cachedIndex{wire: wire, fetchedAt: now}
	a.mu.Unlock()
	return wire, now, false, nil
}

// indexViews converts the wire index to annotated protocol views.
func indexViews(wire storeIndexWire) []gen.StorePluginView {
	views := make([]gen.StorePluginView, 0, len(wire.Store))
	for _, p := range wire.Store {
		versions := make([]gen.StoreVersionView, 0, len(p.Versions))
		for _, v := range p.Versions {
			view := gen.StoreVersionView{
				Version:         v.Version,
				SdkVersion:      v.SdkVersion,
				ProtocolVersion: v.ProtocolVersion,
				MinHostVersion:  v.MinHostVersion,
				PayloadSha256:   v.PayloadSha256,
				Size:            v.Size,
				Changelog:       v.Changelog,
				CreatedAt:       v.CreatedAt,
			}
			checkVersionCompat(&view)
			versions = append(versions, view)
		}
		views = append(views, gen.StorePluginView{
			ID:              p.ID,
			Slug:            p.Slug,
			DisplayName:     p.DisplayName,
			Description:     p.Description,
			Publisher:       p.Publisher,
			Channel:         p.Channel,
			PublisherPubkey: p.PublisherPubkey,
			CurrentVersion:  p.CurrentVersion,
			Versions:        versions,
		})
	}
	return views
}

func communityViews(wire storeIndexWire) []gen.StoreCommunityView {
	views := make([]gen.StoreCommunityView, 0, len(wire.Community))
	for _, c := range wire.Community {
		view := gen.StoreCommunityView{
			ID:              c.ID,
			Slug:            c.Slug,
			DisplayName:     c.DisplayName,
			Description:     c.Description,
			RepoURL:         c.RepoUrl,
			CommitSha:       c.CommitSha,
			Version:         c.Version,
			SdkVersion:      c.SdkVersion,
			ProtocolVersion: c.ProtocolVersion,
			TarballSha256:   c.TarballSha256,
			MirrorURL:       c.MirrorUrl,
			ManifestID:      c.Manifest.ID,
			ManifestName:    c.Manifest.Name,
			Permissions:     c.Manifest.Permissions,
		}
		checkCommunityCompat(&view)
		views = append(views, view)
	}
	return views
}

// joinURL resolves a cloud-relative path against the configured base URL.
func joinURL(base, path string) string {
	b := strings.TrimRight(strings.TrimSpace(base), "/")
	if b == "" {
		return path
	}
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return b + path
}

// httpGetAll fetches a URL and reads the full body (size-capped).
func httpGetAll(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return httpDoAll(client, req)
}

func httpDoAll(client *http.Client, req *http.Request) ([]byte, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadBytes))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d: %s", redactURL(req.URL), resp.StatusCode, snippet(body))
	}
	return body, nil
}

func redactURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	return u.Path
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "...(truncated)"
	}
	return s
}
