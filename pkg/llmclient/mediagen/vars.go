package mediagen

import (
	"net/http"
	"time"

	"github.com/qomos-w/sporemind/pkg/version"
)

// Mutable singletons

// TODO(actor-ownership): migrate to actor-owned state.

// httpClient has no client-level timeout; the total budget is governed by the
// caller's context so that long downloads and polling are not prematurely cut.
var httpClient = &http.Client{}

// defaultUserAgent identifies this client in HTTP requests to media-generation
// providers.
var defaultUserAgent = "SporeMind/" + version.Version

// pollInterval is the delay between task status polls. It defaults to 2s
// (within the 2-5s guideline) and may be shortened for tests.
var pollInterval = 2 * time.Second

// Lookup tables

// successStatuses marks a task as finished successfully.
var successStatuses = map[string]bool{
	"succeeded": true,
	"success":   true,
	"completed": true,
	"complete":  true,
	"done":      true,
	"finished":  true,
}

// failedStatuses marks a task as failed.
var failedStatuses = map[string]bool{
	"failed":    true,
	"error":     true,
	"canceled":  true,
	"cancelled": true,
}

// mediaContainerKeys are nested object/array keys that may hold the artifact.
var mediaContainerKeys = []string{"output", "result", "results", "data", "images", "videos", "url_results"}
