package user

// This file consolidates package-level variable declarations for the user
// package.

import "github.com/qomos-w/gospore/resource"

// --- Resource registry keys ---

// DesktopTokenIssuerKey is the resource registry key under which the user
// actor publishes IssueDesktopToken during OnStart.
var DesktopTokenIssuerKey = resource.NewKey[DesktopTokenIssuer]("sporemind.user.desktop_token_issuer")
