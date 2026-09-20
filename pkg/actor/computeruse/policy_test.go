package computeruse

import (
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestGatedCallables_Declaration asserts every gated callable carries an
// explicit, complete policy declaration (scope + reason).
func TestGatedCallables_Declaration(t *testing.T) {
	want := map[string]string{
		"computeruse.interact":           ScopeInputControl,
		"computeruse.stream":             ScopeContinuousCapture,
		"computeruse.clipboard_get":      ScopeClipboardRead,
		"computeruse.clipboard_get_rich": ScopeClipboardRead,
		"computeruse.clipboard_set":      ScopeClipboardWrite,
		"computeruse.clipboard_set_rich": ScopeClipboardWrite,
	}
	if len(gatedCallables) != len(want) {
		t.Fatalf("gatedCallables has %d entries, want %d", len(gatedCallables), len(want))
	}
	for id, scope := range want {
		g, ok := gatedCallables[id]
		if !ok {
			t.Errorf("%s missing from gatedCallables", id)
			continue
		}
		if g.Scope != scope {
			t.Errorf("%s scope=%q, want %q", id, g.Scope, scope)
		}
		if strings.TrimSpace(g.Reason) == "" {
			t.Errorf("%s has no reason", id)
		}
	}
	for id := range gatedCallables {
		if _, ok := want[id]; !ok {
			t.Errorf("unexpected gated callable %s", id)
		}
	}
}

// TestOnStart_PolicyDeclarations asserts the registration-time policy
// surface: gated callables are explicitly actor.Internal() with their scope
// in the description; read-only callables stay actor.Public().
func TestOnStart_PolicyDeclarations(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.RegOpts = map[string][]actor.RegisterOption{}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	for id, g := range gatedCallables {
		opts, ok := ctx.RegOpts[id]
		if !ok {
			t.Errorf("%s not registered", id)
			continue
		}
		if v := actor.ResolveVisibility(opts...); v != actor.VisibilityInternal {
			t.Errorf("%s visibility=%s, want internal (explicit gate)", id, v)
		}
		if desc := actor.ResolveDescription(opts...); !strings.Contains(desc, g.Scope) {
			t.Errorf("%s description %q does not declare scope %q", id, desc, g.Scope)
		}
	}

	public := []string{
		"computeruse.screenshot",
		"computeruse.capabilities",
		"computeruse.list_windows",
		"computeruse.list_displays",
		"computeruse.list_processes",
		"computeruse.get_window_info",
		"computeruse.cursor_position",
		"computeruse.list_elements",
		"computeruse.ocr",
		"computeruse.setup_ocr",
	}
	for _, id := range public {
		opts, ok := ctx.RegOpts[id]
		if !ok {
			t.Errorf("%s not registered", id)
			continue
		}
		if v := actor.ResolveVisibility(opts...); v != actor.VisibilityPublic {
			t.Errorf("%s visibility=%s, want public", id, v)
		}
	}
}
