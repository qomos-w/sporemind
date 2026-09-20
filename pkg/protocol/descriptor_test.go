package protocol

import (
	"testing"

	spore "github.com/qomos-w/spore/schema"
	appgen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func TestRegisterAppManifest(t *testing.T) {
	m, err := NewManager(StaticFragment{NamespaceOffsets: map[string]uint64{SystemNamespace: 0}})
	if err != nil {
		t.Fatal(err)
	}
	manifest := appgen.AppManifest{ID: "example.app", Namespace: "sporeapp.example.app", Schemas: []appgen.AppSchemaRef{{Name: "Task", Hash: "hash", SchemaID: 1900}}}
	if err := m.RegisterAppManifest(manifest); err != nil {
		t.Fatalf("RegisterAppManifest: %v", err)
	}
	wire, err := m.WireID(manifest.Namespace, 1900)
	if err != nil {
		t.Fatalf("WireID: %v", err)
	}
	if wire == 1900 {
		t.Fatalf("expected external offset, got %d", wire)
	}
	if err := m.UnregisterAppProtocol(manifest.Namespace); err != nil {
		t.Fatalf("unregister: %v", err)
	}
}

func validAppProtocolRegistration() AppProtocolRegistration {
	return AppProtocolRegistration{
		Descriptor: appgen.AppProtocolDescriptor{
			Namespace: "plugin.example.todo", ProtocolVersion: 1, SchemaHash: "hash",
			Schemas:     []appgen.AppSchemaRef{{Name: "Request", Hash: "request-hash"}, {Name: "Response", Hash: "response-hash"}},
			Callables:   []appgen.AppCallableDescriptor{{ID: "todo.list", RequestSchema: "Request", ResponseSchema: "Response"}},
			Events:      []appgen.AppEventDescriptor{{ID: "todo.changed", PayloadSchema: "Response"}},
			Projections: []appgen.AppProjectionDescriptor{{ID: "todo.state", PayloadSchema: "Response"}},
		},
		Schemas: map[string]spore.ObjectDesc{
			"Request":  {Kind: spore.TypeKindStruct, Name: "Request", SchemaID: 1900},
			"Response": {Kind: spore.TypeKindStruct, Name: "Response", SchemaID: 1901},
		},
	}
}

func TestValidateAppProtocolFailsClosedOnSchemaConflicts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*AppProtocolRegistration)
	}{
		{"unknown callable request", func(r *AppProtocolRegistration) { r.Descriptor.Callables[0].RequestSchema = "Missing" }},
		{"unknown callable response", func(r *AppProtocolRegistration) { r.Descriptor.Callables[0].ResponseSchema = "Missing" }},
		{"unknown event payload", func(r *AppProtocolRegistration) { r.Descriptor.Events[0].PayloadSchema = "Missing" }},
		{"unknown projection payload", func(r *AppProtocolRegistration) { r.Descriptor.Projections[0].PayloadSchema = "Missing" }},
		{"duplicate schema name", func(r *AppProtocolRegistration) {
			r.Descriptor.Schemas = append(r.Descriptor.Schemas, r.Descriptor.Schemas[0])
		}},
		{"duplicate schema id", func(r *AppProtocolRegistration) {
			d := r.Schemas["Response"]
			d.SchemaID = r.Schemas["Request"].SchemaID
			r.Schemas["Response"] = d
		}},
		{"descriptor name mismatch", func(r *AppProtocolRegistration) {
			d := r.Schemas["Response"]
			d.Name = "Other"
			r.Schemas["Response"] = d
		}},
		{"undeclared descriptor", func(r *AppProtocolRegistration) {
			r.Schemas["Extra"] = spore.ObjectDesc{Kind: spore.TypeKindStruct, Name: "Extra", SchemaID: 1902}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := validAppProtocolRegistration()
			tt.mutate(&reg)
			if err := ValidateAppProtocol(reg); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestRegisterAppProtocol(t *testing.T) {
	m, err := NewManager(StaticFragment{NamespaceOffsets: map[string]uint64{SystemNamespace: 0}})
	if err != nil {
		t.Fatal(err)
	}
	reg := AppProtocolRegistration{Descriptor: appgen.AppProtocolDescriptor{Namespace: "plugin.example.todo", ProtocolVersion: 1, SchemaHash: "hash", Schemas: []appgen.AppSchemaRef{{Name: "Task", Hash: "task-hash"}}}, Schemas: map[string]spore.ObjectDesc{"Task": {Kind: spore.TypeKindStruct, Name: "Task", SchemaID: 1900}}}
	if err := m.RegisterAppProtocol(reg); err != nil {
		t.Fatalf("RegisterAppProtocol: %v", err)
	}
	if err := m.UnregisterAppProtocol(reg.Descriptor.Namespace); err != nil {
		t.Fatalf("UnregisterAppProtocol: %v", err)
	}
}
