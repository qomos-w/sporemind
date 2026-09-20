package computeruse

import "github.com/qomos-w/sporemind/pkg/actor/computeruse/internal/capture/ocr"

// --- OCR setup ---

// ensurePaddleModels is the setup_ocr download/verify entry point. It is a
// package variable so handler tests can stub out the network fetch.
//
// TODO(actor-ownership): migrate to actor-owned state.
var ensurePaddleModels = ocr.EnsurePaddleModels

// --- policy gating ---

// gatedCallables is the complete set of non-public computeruse callables
// with their required policy scope. handleCapabilities surfaces this table
// (callable.* entries) so agents can introspect the gating; the
// registration test asserts it stays in sync with the actor surface.
var gatedCallables = map[string]gatedCallable{
	"computeruse.interact": {
		Scope:  ScopeInputControl,
		Reason: "drives the real mouse/keyboard and can close or move arbitrary windows",
	},
	"computeruse.stream": {
		Scope:  ScopeContinuousCapture,
		Reason: "continuously captures screen contents beyond a single user-initiated screenshot",
	},
	"computeruse.clipboard_get": {
		Scope:  ScopeClipboardRead,
		Reason: "clipboard contents may hold user secrets (passwords, tokens)",
	},
	"computeruse.clipboard_get_rich": {
		Scope:  ScopeClipboardRead,
		Reason: "clipboard contents may hold user secrets in any format (text/image/files/html)",
	},
	"computeruse.clipboard_set": {
		Scope:  ScopeClipboardWrite,
		Reason: "overwrites the user's clipboard contents",
	},
	"computeruse.clipboard_set_rich": {
		Scope:  ScopeClipboardWrite,
		Reason: "overwrites the user's clipboard, including file-list payloads",
	},
}