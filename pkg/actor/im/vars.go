package im

// This file consolidates package-level variable declarations for the im
// package.

import "fmt"

// ServiceName is the service domain every im callable is registered under.
const ServiceName = "im"

// Account/connection status values reported by im.status (mirrors the
// schemas/im._3042.spore ImAccountView.Status enumeration).
const (
	statusConnected    = "connected"
	statusDisconnected = "disconnected"
)

// errSendNotImplemented is returned by the im.send stub until provider
// adapters are wired (route/adapter cards land after the skeleton).
var errSendNotImplemented = fmt.Errorf("im.send: not implemented yet (no provider adapter connected)")
