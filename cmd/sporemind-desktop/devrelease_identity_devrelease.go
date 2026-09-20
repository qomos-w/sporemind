//go:build devrelease

package main

// devReleaseBuild marks `make dev-release` desktop builds (build tag
// "devrelease"): dev semantics (BuildType=dev) but a distinct instance
// identity — gateway port 18081, data dir .sporemind-devrelease — so its
// gateway listener and data dir never collide with a dev instance. The
// artifact is named sporemind.exe (same path as build-desktop), but startup
// never kills processes, so same-named builds of other flavors are not
// affected. ClosePreviousInstance remains port-scoped to the 18081 flavor.
const devReleaseBuild = true
