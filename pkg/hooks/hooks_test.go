package hooks

import (
	"errors"
	"testing"
)

func TestFire_NoMiddleware_Passthrough(t *testing.T) {
	h := New[int]()
	result, err := h.Fire(42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 42 {
		t.Fatalf("expected 42, got %d", result)
	}
}

func TestFire_AppliesMiddleware(t *testing.T) {
	h := New[int]()
	h.Use(func(v int) (int, error) { return v + 1, nil })
	h.Use(func(v int) (int, error) { return v * 2, nil })
	result, err := h.Fire(5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// (5+1)*2 = 12
	if result != 12 {
		t.Fatalf("expected 12, got %d", result)
	}
}

func TestFire_ShortCircuitsOnError(t *testing.T) {
	h := New[int]()
	expectedErr := errors.New("boom")
	h.Use(func(v int) (int, error) { return v + 1, nil })
	h.Use(func(v int) (int, error) { return v, expectedErr }) // pass-through on error
	h.Use(func(v int) (int, error) { return v * 2, nil })
	result, err := h.Fire(5)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected boom, got %v", err)
	}
	// (5+1)=6 applied, error middleware passes through, *2 should not run
	if result != 6 {
		t.Fatalf("expected partial result 6, got %d", result)
	}
}

func TestUse_MultipleRegistration(t *testing.T) {
	h := New[int]()
	h.Use(
		func(v int) (int, error) { return v + 10, nil },
		func(v int) (int, error) { return v + 20, nil },
	)
	result, err := h.Fire(0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 30 {
		t.Fatalf("expected 30, got %d", result)
	}
}

func TestFire_SliceType(t *testing.T) {
	h := New[[]string]()
	h.Use(func(v []string) ([]string, error) {
		return append(v, "a"), nil
	})
	h.Use(func(v []string) ([]string, error) {
		return append(v, "b"), nil
	})
	result, err := h.Fire([]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 || result[0] != "a" || result[1] != "b" {
		t.Fatalf("expected [a b], got %v", result)
	}
}

func TestFire_StructType(t *testing.T) {
	type event struct {
		Name  string
		Count int
	}
	h := New[event]()
	h.Use(func(e event) (event, error) {
		e.Count++
		return e, nil
	})
	result, err := h.Fire(event{Name: "test", Count: 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Count != 1 {
		t.Fatalf("expected count 1, got %d", result.Count)
	}
}

func TestFireWith_CallsOnStep(t *testing.T) {
	h := New[int]()
	h.Use(func(v int) (int, error) { return v + 1, nil })
	h.Use(func(v int) (int, error) { return v * 2, nil })

	var steps []int
	result, err := h.FireWith(5, FireConfig{
		OnStep: func(index int, err error) {
			if err != nil {
				t.Fatalf("unexpected error at step %d: %v", index, err)
			}
			steps = append(steps, index)
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 12 {
		t.Fatalf("expected 12, got %d", result)
	}
	if len(steps) != 2 || steps[0] != 0 || steps[1] != 1 {
		t.Fatalf("expected steps [0, 1], got %v", steps)
	}
}

func TestFireWith_OnStepOnError(t *testing.T) {
	h := New[int]()
	expectedErr := errors.New("boom")
	h.Use(func(v int) (int, error) { return v + 1, nil })
	h.Use(func(v int) (int, error) { return 0, expectedErr })

	var steps []int
	var gotErr error
	result, err := h.FireWith(5, FireConfig{
		OnStep: func(index int, stepErr error) {
			steps = append(steps, index)
			if stepErr != nil {
				gotErr = stepErr
			}
		},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if gotErr == nil {
		t.Fatal("expected OnStep to receive error")
	}
	if len(steps) != 2 || steps[0] != 0 || steps[1] != 1 {
		t.Fatalf("expected steps [0, 1], got %v", steps)
	}
	_ = result
}

func TestFireWith_NoCallback(t *testing.T) {
	h := New[int]()
	h.Use(func(v int) (int, error) { return v + 1, nil })
	result, err := h.FireWith(5, FireConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 6 {
		t.Fatalf("expected 6, got %d", result)
	}
}
