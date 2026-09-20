//go:build !release && !devrelease

package codegen

// Dev builds (no release/devrelease tag) carry no embedded SDK: findDevSDK
// resolves the checkout copy, so the release asset (pkg/codegen/sdk.zip) is
// not needed and its absence must not break compilation.

var embeddedSDKZip []byte

const embeddedSDKChecksum = ""
