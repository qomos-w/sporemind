package imagegen

import (
	"net/http"

	"github.com/qomos-w/sporemind/pkg/version"
)

// Mutable singletons

// TODO(actor-ownership): migrate to actor-owned state.

// httpClient is the image-generation HTTP client. It deliberately sets no
// client-level Timeout: every call is bounded by the caller-supplied context
// (3min on the aggregator path, 10min on the media-account path), and a fixed
// client timeout would preempt that budget and abort long synchronous
// generations early.
var httpClient = &http.Client{}

// defaultUserAgent identifies this client in HTTP requests to image-generation
// providers.
var defaultUserAgent = "SporeMind/" + version.Version
