package authz

import (
	"github.com/qomos-w/sporemind/pkg/auth"
)

// Mutable singletons
// TODO(actor-ownership): migrate to actor-owned state
var defaultManager = auth.NewManager(auth.DefaultJWTConfig())
