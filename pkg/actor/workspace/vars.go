package workspace

// This file consolidates package-level variable declarations for the workspace
// package.

import (
	"embed"
	"time"
)

// --- Crawl execution ---

// crawlPollInterval is the status polling interval used by the crawl executor.
// It is a package-level variable so tests can shorten it.
//
// TODO(actor-ownership): migrate to actor-owned state
var crawlPollInterval = 500 * time.Millisecond

// --- Native scaffold templates ---

// nativeScaffoldFS embeds the plugin scaffold templates (app.appdef,
// go.mod, README.md, and the vendored minimal ABI layer under vendor-sdk/)
// so the scaffold never reads the repository filesystem at runtime. The
// generated artifacts (main.gen.go, handlers.go, schemas_gen.go,
// app.manifest.json, client.gen.ts) are produced by codegen.Generate.
//
//go:embed scaffoldtemplates/native
var nativeScaffoldFS embed.FS

// --- Builtin agent kind pruning ---

// retiredBuiltinKinds lists builtin agent kinds that have been removed from the
// codebase. Persisted state from older versions may still carry their configs
// and agent references; seedAgentKindConfigs prunes them so they do not linger
// as un-creatable, un-deletable ghost entries. Custom user-defined kinds are
// never pruned.
var retiredBuiltinKinds = map[string]bool{
	"architect": true,
	"grapher":   true,
	"oracle":    true,
}
