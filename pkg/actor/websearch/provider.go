package websearch

import (
	"context"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// SearchOpts carries per-request parameters for a web search.
type SearchOpts struct {
	MaxResults int    // default 10, max 50
	TimeRange  string // "oneDay" | "oneWeek" | "oneMonth" | "oneYear" | "noLimit"
	Engine     string // provider-specific engine override
	ApiKey     string // resolved credential
	Endpoint   string // custom endpoint (self-hosted providers)
}

// SearchProvider is the adapter interface for a web search backend.
type SearchProvider interface {
	// ID returns the provider identifier (e.g. "zhipu", "tavily").
	ID() string
	// Name returns the human-readable display name.
	Name() string
	// RequiresKey reports whether this provider needs an API key.
	RequiresKey() bool
	// Search executes a web search and returns structured results.
	Search(ctx context.Context, query string, opts SearchOpts) ([]gen.WebSearchResult, error)
}
