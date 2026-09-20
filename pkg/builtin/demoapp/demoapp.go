// Package demoapp provides a built-in example SporeApp that demonstrates
// the full app lifecycle: callable invocation, event declaration,
// entrypoint registration, state management, and agent binding.
//
// It serves as both a reference implementation and an acceptance-test
// fixture for the AppManager / SporeApp runtime chain.
package demoapp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	sporesch "github.com/qomos-w/spore/schema"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// RegisterReq builds an AppManagerRegisterReq for this demo app, suitable
// for passing to appmanager.register in tests.
func RegisterReq() gen.AppManagerRegisterReq {
	descriptor := gen.AppObjectDescriptor{
		Kind: string(sporesch.TypeKindStruct), Name: "DemoEchoReq", SchemaID: int64(gen.DemoEchoReqSchemaID),
		Fields: []gen.AppFieldDescriptor{
			{Name: "Text", Type: gen.AppTypeDescriptor{Kind: string(sporesch.TypeKindScalar), Name: "string"}},
			{Name: "N", Type: gen.AppTypeDescriptor{Kind: string(sporesch.TypeKindScalar), Name: "int"}},
		},
	}
	object := sporesch.ObjectDesc{
		Kind: sporesch.TypeKindStruct, Name: descriptor.Name, SchemaID: gen.DemoEchoReqSchemaID,
		Fields: []sporesch.FieldDesc{
			{Name: "Text", Type: sporesch.TypeDesc{Kind: sporesch.TypeKindScalar, Name: "string"}},
			{Name: "N", Type: sporesch.TypeDesc{Kind: sporesch.TypeKindScalar, Name: "int"}},
		},
	}
	encoded, _ := json.Marshal(object)
	hash := sha256.Sum256(encoded)
	manifest := Manifest
	manifest.Schemas = append([]gen.AppSchemaRef(nil), Manifest.Schemas...)
	manifest.Schemas[0].Hash = hex.EncodeToString(hash[:])
	return gen.AppManagerRegisterReq{
		Manifest: manifest, EntryModule: EntryModule, Modules: Modules,
		SchemaDescriptors: map[string]gen.AppObjectDescriptor{descriptor.Name: descriptor},
	}
}
