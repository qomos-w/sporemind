package hooks

import (
	"errors"
	"testing"
)

func TestRegisterAndLookup(t *testing.T) {
	f := Factory(func(params map[string]any) (any, error) {
		return "result", nil
	})
	Register("test_hook_1", "test_mw_1", f)
	defer resetGlobal()

	got, ok := Lookup("test_hook_1", "test_mw_1")
	if !ok {
		t.Fatal("expected to find registered factory")
	}
	val, err := got(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "result" {
		t.Fatalf("expected 'result', got %v", val)
	}
}

func TestLookup_NotFound(t *testing.T) {
	_, ok := Lookup("nonexistent_hook", "nonexistent_mw")
	if ok {
		t.Fatal("expected not found")
	}
}

func TestLookup_HookExists_MiddlewareNotFound(t *testing.T) {
	Register("test_hook_2", "existing", func(map[string]any) (any, error) { return nil, nil })
	defer resetGlobal()

	_, ok := Lookup("test_hook_2", "nonexistent")
	if ok {
		t.Fatal("expected not found for unknown middleware")
	}
}

func TestRegister_Overwrite(t *testing.T) {
	v1 := Factory(func(map[string]any) (any, error) { return "v1", nil })
	v2 := Factory(func(map[string]any) (any, error) { return "v2", nil })
	Register("test_hook_3", "mw", v1)
	defer resetGlobal()
	Register("test_hook_3", "mw", v2)

	got, _ := Lookup("test_hook_3", "mw")
	val, _ := got(nil)
	if val != "v2" {
		t.Fatalf("expected overwrite to v2, got %v", val)
	}
}

func TestFactoryError(t *testing.T) {
	expectedErr := errors.New("factory failed")
	Register("test_hook_4", "bad", func(map[string]any) (any, error) { return nil, expectedErr })
	defer resetGlobal()

	got, _ := Lookup("test_hook_4", "bad")
	_, err := got(nil)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected factory error, got %v", err)
	}
}

func TestHookNames(t *testing.T) {
	Register("alpha", "a1", func(map[string]any) (any, error) { return nil, nil })
	Register("beta", "b1", func(map[string]any) (any, error) { return nil, nil })
	Register("alpha", "a2", func(map[string]any) (any, error) { return nil, nil })
	defer resetGlobal()

	names := HookNames()
	if len(names) != 2 {
		t.Fatalf("expected 2 hook names, got %d: %v", len(names), names)
	}
}

// resetGlobal clears the global registry between tests.
func resetGlobal() {
	global = &Registry{entries: make(map[string]map[string]Factory)}
}
