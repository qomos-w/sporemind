package computeruse

import (
	"reflect"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestRegistrationSurface_Declarations verifies that every callable in the
// computeruse actor declares the correct WithEffect and WithService values.
func TestRegistrationSurface_Declarations(t *testing.T) {
	wantEffect := map[string]string{
		"computeruse.screenshot":         "none",
		"computeruse.list_windows":       "none",
		"computeruse.list_displays":      "none",
		"computeruse.list_processes":     "none",
		"computeruse.get_window_info":    "none",
		"computeruse.cursor_position":    "none",
		"computeruse.list_elements":      "none",
		"computeruse.ocr":                "none",
		"computeruse.capabilities":       "none",
		"computeruse.stream":             "none",
		"computeruse.clipboard_get":      "none",
		"computeruse.clipboard_get_rich": "none",
		"computeruse.interact":           "irreversible",
		"computeruse.clipboard_set":      "irreversible",
		"computeruse.clipboard_set_rich": "irreversible",
		"computeruse.setup_ocr":          "irreversible",
	}

	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.RegOpts = map[string][]actor.RegisterOption{}

	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	for id, wantEff := range wantEffect {
		opts, ok := ctx.RegOpts[id]
		if !ok {
			t.Errorf("%s not registered", id)
			continue
		}
		if got := actor.ResolveEffect(opts...); got != wantEff {
			t.Errorf("%s EffectKind = %q, want %q", id, got, wantEff)
		}
	}
}

// TestRegistrationSurface_HandlerModes pins the read-only OS query callables
// to the stateless (PureContext) pool: the gospore cell infers ModeStateless
// from the first parameter, so a stateful Context there would silently put
// every frontend/agent OS poll back on the owner lane. setup_ocr is the one
// exception — it writes durable OCR state — so it keeps the stateful
// signature and is routed to its own dedicated computeruse_setup lane.
func TestRegistrationSurface_HandlerModes(t *testing.T) {
	pure := []string{
		"computeruse.list_windows",
		"computeruse.list_displays",
		"computeruse.list_processes",
		"computeruse.list_elements",
		"computeruse.cursor_position",
		"computeruse.get_window_info",
		"computeruse.capabilities",
		"computeruse.ocr",
		"computeruse.clipboard_get",
		"computeruse.clipboard_get_rich",
	}

	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.RegOpts = map[string][]actor.RegisterOption{}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	pureType := reflect.TypeOf((*actor.PureContext)(nil)).Elem()
	for _, id := range pure {
		handler, ok := ctx.Regs[id]
		if !ok {
			t.Errorf("%s handler not recorded", id)
			continue
		}
		fnType := reflect.TypeOf(handler)
		if fnType.NumIn() < 1 || fnType.In(0) != pureType {
			t.Errorf("%s first param = %v, want actor.PureContext", id, fnType.In(0))
		}
		if got := actor.ResolveLoop(ctx.RegOpts[id]...); got != "" {
			t.Errorf("%s loop = %q, want default pure loop (empty loop option)", id, got)
		}
	}

	if got := actor.ResolveLoop(ctx.RegOpts["computeruse.setup_ocr"]...); got != "computeruse_setup" {
		t.Errorf("setup_ocr loop = %q, want computeruse_setup", got)
	}
	if mode, ok := ctx.Loops["computeruse_setup"]; !ok || mode != actor.ModeStateful {
		t.Errorf("computeruse_setup loop mode = %v (present=%v), want ModeStateful", mode, ok)
	}
}
