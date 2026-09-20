package appbinding

import "testing"

func TestRegistrySurface(t *testing.T) {
	r := NewRegistry()
	if err := r.BindSurface(SurfaceBinding{AppID: "app", AgentID: "agent", Entrypoint: "main"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Surface("app", "agent"); !ok {
		t.Fatal("surface binding missing")
	}
}

func TestRegistryHasBinding(t *testing.T) {
	r := NewRegistry()
	// No bindings yet.
	if r.HasBinding("app", "agent") {
		t.Fatal("expected no binding before any bind")
	}
	// Surface binding alone satisfies HasBinding.
	if err := r.BindSurface(SurfaceBinding{AppID: "app", AgentID: "agent", Entrypoint: "main"}); err != nil {
		t.Fatal(err)
	}
	if !r.HasBinding("app", "agent") {
		t.Fatal("expected binding after surface bind")
	}
	// Different agent has no binding.
	if r.HasBinding("app", "other") {
		t.Fatal("expected no binding for unbound agent")
	}
	// Unbind removes it.
	r.Unbind("app", "agent")
	if r.HasBinding("app", "agent") {
		t.Fatal("expected no binding after unbind")
	}
}

func TestRegistryUnbindSurface(t *testing.T) {
	r := NewRegistry()
	if err := r.BindSurface(SurfaceBinding{AppID: "app", AgentID: "agent", Entrypoint: "main"}); err != nil {
		t.Fatal(err)
	}
	r.Unbind("app", "agent")
	if _, ok := r.Surface("app", "agent"); ok {
		t.Fatal("surface binding should be removed after Unbind")
	}
}