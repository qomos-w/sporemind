package hooks

// Middleware transforms an event of type E, returning the (possibly modified)
// event and an error. Returning a non-nil error short-circuits the chain.
type Middleware[E any] func(E) (E, error)

// FireConfig controls per-step logging during FireWith.
type FireConfig struct {
	// OnStep is called after each middleware runs. index is the 0-based
	// position in the chain; err is non-nil when the middleware failed.
	OnStep func(index int, err error)
}

// Hook is an ordered chain of Middleware[E]. Zero value is safe to use.
type Hook[E any] struct {
	chain []Middleware[E]
}

// New returns a new Hook with no middleware registered.
func New[E any]() *Hook[E] {
	return &Hook[E]{}
}

// Use appends middleware to the chain. Middleware runs in registration order.
func (h *Hook[E]) Use(m ...Middleware[E]) {
	h.chain = append(h.chain, m...)
}

// Fire passes the event through each middleware in order.
// Stops at the first error and returns the partially-transformed event.
func (h *Hook[E]) Fire(e E) (E, error) {
	var err error
	for _, m := range h.chain {
		if e, err = m(e); err != nil {
			return e, err
		}
	}
	return e, nil
}

// FireWith is like Fire but calls cfg.OnStep after each middleware.
// OnStep receives the middleware index and any error returned by that step.
func (h *Hook[E]) FireWith(e E, cfg FireConfig) (E, error) {
	var err error
	for i, m := range h.chain {
		if e, err = m(e); err != nil {
			if cfg.OnStep != nil {
				cfg.OnStep(i, err)
			}
			return e, err
		}
		if cfg.OnStep != nil {
			cfg.OnStep(i, nil)
		}
	}
	return e, nil
}
