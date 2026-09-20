//go:build release || devrelease

package codegen

// Release builds (and `make dev-release` desktop builds, tag "devrelease")
// embed the packed SDK source (cmd/tools/sdkzip output) so plugin dev
// tooling works on machines with no sporemind checkout. The zip is produced
// by `make build-sdk-asset` before the build; build failure here means the
// asset step was skipped.
//
// The go.mod checksum var stays empty in this mode: sdk_workspace.go verifies
// it only when non-empty, and release extraction re-verifies via build success.

import _ "embed"

//go:embed sdk.zip
var embeddedSDKZip []byte

const embeddedSDKChecksum = ""
