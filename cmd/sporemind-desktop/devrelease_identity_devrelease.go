//go:build devrelease

package main

// devReleaseBuild marks `make dev-release` desktop builds (build tag
// "devrelease"): dev semantics (BuildType=dev) but a distinct instance
// identity — an OS-assigned ephemeral gateway port (the bound address is
// recorded in the data dir's gateway.port file) and data dir
// .sporemind-devrelease — so its gateway listener and data dir never collide
// with a dev instance. The artifact is named sporemind.exe (same path as
// build-desktop), but startup never kills processes, so same-named builds of
// other flavors are not affected. ClosePreviousInstance stays flavor-scoped:
// it only ever reaches the address recorded by a previous dev-release
// instance.
const devReleaseBuild = true
