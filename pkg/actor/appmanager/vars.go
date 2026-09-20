package appmanager

// This file consolidates package-level variable declarations for the
// appmanager package.

import (
	sporesch "github.com/qomos-w/spore/schema"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// --- Gate ordering ---

// gateOrder is the canonical order used in reports.
var gateOrder = []GateName{GateBuild, GateStubFill, GateManifest, GateCoverage, GateVendor, GateFrontend, GateBundle}

// --- Spore app invocation ---

// sporeAppInvokeRespDesc describes the SporeAppInvokeResp envelope for
// BinaryCodec decoding when the reply frame body is TBC-framed.
var sporeAppInvokeRespDesc = sporesch.TypeDesc{
	Kind:   sporesch.TypeKindStruct,
	Name:   "SporeAppInvokeResp",
	TypeID: sporesch.TypeID(gen.SporeAppInvokeRespSchemaID),
}
