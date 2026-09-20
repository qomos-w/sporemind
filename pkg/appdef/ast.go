// Package appdef parses .appdef declaration files for plugin development.
//
// An .appdef file wraps spore-compatible struct declarations inside an app
// block alongside declarative callable/entrypoint/event/bundle declarations.
//
// The struct syntax is 100% compatible with the main repository schemas/*.spore
// schema profile (struct, optional, array<T>, map<K,V>, scalars, struct references,
// type aliases). Struct blocks are extracted and parsed via the same spore parser
// used for .spore files, ensuring semantic equivalence.
//
// The wrapper layer (app/callable/entrypoint/event/bundle) uses a simple
// declarative key-value + block grammar with no expression evaluation.
package appdef

import (
	"fmt"

	"github.com/qomos-w/spore/schema"
)

// --- AST Types ---

// AppDef is the root AST node of a parsed .appdef file.
type AppDef struct {
	AppMeta      AppMeta
	Structs      []schema.ObjectDesc // Parsed via spore script parser
	TypeAliases  []TypeAliasDecl     // Declared type aliases
	Callables    []CallableDecl
	Entrypoints  []EntrypointDecl
	Events       []EventDecl
	Listens      []ListenDecl
	Bundles      []BundleDecl
	Dependencies []DependencyDecl
	FreeAgent    *FreeAgentDecl
	PluginAgents []*PluginAgentDecl // in declaration order; slot names unique
	SchemaSource string // Combined struct/alias source text suitable for codegen
}

// TypeAliasDecl represents a `type Alias = Target` schema declaration.
type TypeAliasDecl struct {
	Name   string
	Target string
	Line   int
}

// AppMeta holds the top-level app block key-value metadata.
type AppMeta struct {
	ID          string
	Name        string
	Version     string
	Namespace   string
	Permissions []string
}

// CallableDecl represents a callable { ... } block inside the app block.
type CallableDecl struct {
	ID        string
	Request   string // struct name (empty = void)
	Response  string // struct name (empty = void, generator uses "void")
	Effect    string
	ToolName  string
	Service   string
	Streaming bool
	TimeoutMs int64 // timeout in milliseconds; 0 = not declared
	// Description comes from the contiguous `//` comment lines directly
	// above the callable block declaration (doc-comment convention); empty
	// when no such comment exists. Flows into the manifest descriptor and
	// renders in the PluginToolbar capability panel.
	Description string
	// Expose controls which consumer surfaces may call this callable:
	// "frontend" (panel only), "agent" (LLM tool only), or "both" (default
	// when empty, consumed as-is — the manifest omits the default rather
	// than normalizing it, mirroring Effect/ToolName).
	Expose string
	// Watch lists declared event IDs whose emission invalidates cached
	// results of this callable (cache-invalidation hint). Nil/empty when
	// not declared.
	Watch []string
	Line  int // source line number for diagnostics
}

// EntrypointDecl represents an entrypoint view main { ... } block.
type EntrypointDecl struct {
	Kind  string // e.g. "view"
	ID    string
	Title string
	Route string
	Line  int
}

// EventDecl represents an event { ... } block.
type EventDecl struct {
	ID         string
	Payload    string // struct name
	Permission string
	Line       int
}

// ListenDecl represents a `listen <kind> { }` block — an inbound host
// event the app subscribes to. Symmetric with callable (callable=in,
// event=out, listen=in): codegen emits the host event payload type and an
// OnXxx handler stub; the pluginhost forwards matching bus events into the
// plugin via a reserved invoke name. The authoritative kind→payload
// catalog lives in pkg/appbinding (EventCatalog); appdef validates shape
// and dedup only.
type ListenDecl struct {
	Kind string
	Line int
}

// BundleDecl represents a bundle { ... } block.
type BundleDecl struct {
	Title       string
	Description string
	// Icon is a host icon-library name (query appmanager.icon_names); empty
	// falls back to the "package" default at publish time.
	Icon string
	// Color is an optional hex color (e.g. "#9333ea") for the published bundle
	// card visual; empty lets the consumer derive a stable color from the name.
	Color string
	Tools []string // callable IDs
	Line  int
}

// DependencyDecl represents a dependency { ... } block.
type DependencyDecl struct {
	ID      string
	Version string
	Line    int
}

// FreeAgentDecl represents a free_agent { ... } block (maps to
// gen.FreeAgentBinding). It is standalone: the app can create/switch/message
// agents per this policy without binding to a fixed agent.
type FreeAgentDecl struct {
	AllowCreate  bool
	AllowSwitch  bool
	AllowMessage bool
	AgentKinds   []string
	Line         int
}

// PluginAgentDecl represents a plugin_agent [name] { ... } block (maps to
// gen.PluginAgentBinding): the app requests a dedicated workspace-global
// agent provisioned by the host at registration time. The agent is created
// in the same workspace registry as the Coordinator and auto-mounts the
// app's own bundles plus the optional extra builtin bundle IDs listed here.
// An app may declare multiple plugin_agent blocks; Name is the binding slot
// ("default" for a block without a name header) and must be unique per app.
type PluginAgentDecl struct {
	// Name is the binding slot: [a-z][a-z0-9-]*. The parser normalizes an
	// unnamed block to "default".
	Name string
	// DisplayName is the agent's display name; empty falls back to the app Name.
	DisplayName string
	// SystemPrompt is an optional per-app role override appended after the
	// builtin plugin-agent profile prompt. Limited to 8192 bytes per block.
	SystemPrompt string
	// Bundles lists extra builtin bundle card IDs (e.g.
	// "builtin:bundle:web-search") mounted on the agent in addition to the
	// app's own app-bundle cards. Existence is validated at mount time, not
	// here — appdef has no builtin-card catalog.
	Bundles []string
	// Model binds the slot agent's primary model slot to a specific unit at
	// registration time, encoded as "Provider|Model" (the unit key convention
	// shared with in-app ModelDefaults). Empty leaves the host default in
	// place; the parser only validates the encoding shape, not provider
	// existence.
	Model string
	Line   int
}

// Diagnostic represents a structured parse/validation error with a line number.
type Diagnostic struct {
	Line    int
	Message string
}

func (d Diagnostic) Error() string {
	if d.Line > 0 {
		return formatError(d.Line, d.Message)
	}
	return d.Message
}

func formatError(line int, msg string) string {
	return fmt.Sprintf("line %d: %s", line, msg)
}
