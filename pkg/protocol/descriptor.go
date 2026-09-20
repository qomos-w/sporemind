package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	spore "github.com/qomos-w/spore/schema"
	appgen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

type AppProtocolRegistration struct {
	Descriptor appgen.AppProtocolDescriptor
	Schemas    map[string]spore.ObjectDesc
}

func AppObjectDescriptors(descriptors map[string]appgen.AppObjectDescriptor) (map[string]spore.ObjectDesc, error) {
	objects := make(map[string]spore.ObjectDesc, len(descriptors))
	for name, descriptor := range descriptors {
		if name == "" || descriptor.Name != name || descriptor.SchemaID <= 0 {
			return nil, fmt.Errorf("app schema descriptor %q has invalid name or schema ID", name)
		}
		fields := make([]spore.FieldDesc, len(descriptor.Fields))
		for i, field := range descriptor.Fields {
			if field.Name == "" {
				return nil, fmt.Errorf("app schema descriptor %q has unnamed field", name)
			}
			typ, err := appTypeDescriptor(field.Type)
			if err != nil {
				return nil, fmt.Errorf("app schema descriptor %q field %q: %w", name, field.Name, err)
			}
			fields[i] = spore.FieldDesc{Name: field.Name, Type: typ, Description: field.Description, Private: field.Private, Optional: field.Optional}
		}
		objects[name] = spore.ObjectDesc{Kind: spore.TypeKind(descriptor.Kind), Name: descriptor.Name, Fields: fields, SchemaID: uint64(descriptor.SchemaID)}
	}
	return objects, nil
}

func appTypeDescriptor(descriptor appgen.AppTypeDescriptor) (spore.TypeDesc, error) {
	if descriptor.Kind == "" {
		return spore.TypeDesc{}, fmt.Errorf("type kind is required")
	}
	result := spore.TypeDesc{Kind: spore.TypeKind(descriptor.Kind), Name: descriptor.Name, TypeID: spore.TypeID(descriptor.TypeID), ClassName: descriptor.ClassName, ClassID: uint64(descriptor.ClassID)}
	var err error
	if descriptor.Element != nil {
		value, convertErr := appTypeDescriptor(*descriptor.Element)
		if convertErr != nil {
			return spore.TypeDesc{}, convertErr
		}
		result.Element = &value
	}
	if descriptor.Key != nil {
		value, convertErr := appTypeDescriptor(*descriptor.Key)
		if convertErr != nil {
			return spore.TypeDesc{}, convertErr
		}
		result.Key = &value
	}
	if descriptor.Value != nil {
		value, convertErr := appTypeDescriptor(*descriptor.Value)
		if convertErr != nil {
			return spore.TypeDesc{}, convertErr
		}
		result.Value = &value
	}
	return result, err
}

func ValidateAppSchemaDescriptors(manifest appgen.AppManifest, descriptors map[string]appgen.AppObjectDescriptor) (AppProtocolRegistration, error) {
	objects, err := AppObjectDescriptors(descriptors)
	if err != nil {
		return AppProtocolRegistration{}, err
	}
	refs := make(map[string]appgen.AppSchemaRef, len(manifest.Schemas))
	for _, ref := range manifest.Schemas {
		refs[ref.Name] = ref
		object, ok := objects[ref.Name]
		if !ok {
			return AppProtocolRegistration{}, fmt.Errorf("app schema %q descriptor is missing", ref.Name)
		}
		encoded, marshalErr := json.Marshal(object)
		if marshalErr != nil {
			return AppProtocolRegistration{}, marshalErr
		}
		hash := sha256.Sum256(encoded)
		if !strings.EqualFold(ref.Hash, hex.EncodeToString(hash[:])) {
			return AppProtocolRegistration{}, fmt.Errorf("app schema %q hash mismatch", ref.Name)
		}
	}
	if len(refs) != len(objects) {
		return AppProtocolRegistration{}, fmt.Errorf("app protocol contains undeclared schema descriptors")
	}
	names := make([]string, 0, len(objects))
	for name := range objects {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		encoded, marshalErr := json.Marshal(objects[name])
		if marshalErr != nil {
			return AppProtocolRegistration{}, marshalErr
		}
		h.Write(encoded)
	}
	return AppProtocolRegistration{Descriptor: appgen.AppProtocolDescriptor{Namespace: manifest.Namespace, ProtocolVersion: manifest.ProtocolVersion, SchemaHash: hex.EncodeToString(h.Sum(nil)), Abi: appgen.AppAbiDescriptor{Name: "sporemind.app", Version: manifest.ProtocolVersion, Encoding: "tbc"}, Schemas: manifest.Schemas, Callables: manifest.Callables, Events: manifest.Events, Projections: manifest.Projections}, Schemas: objects}, nil
}

func ValidateAppProtocol(reg AppProtocolRegistration) error {
	d := reg.Descriptor
	if d.Namespace == "" || d.ProtocolVersion <= 0 || d.SchemaHash == "" {
		return fmt.Errorf("app protocol metadata is incomplete")
	}
	schemaNames := make(map[string]struct{}, len(d.Schemas))
	schemaIDs := make(map[uint64]string, len(d.Schemas))
	for _, ref := range d.Schemas {
		if ref.Name == "" || ref.Hash == "" {
			return fmt.Errorf("app protocol schema reference is incomplete")
		}
		if _, exists := schemaNames[ref.Name]; exists {
			return fmt.Errorf("duplicate app protocol schema %q", ref.Name)
		}
		obj, ok := reg.Schemas[ref.Name]
		if !ok {
			return fmt.Errorf("app protocol schema %q descriptor is missing", ref.Name)
		}
		if obj.SchemaID == 0 {
			return fmt.Errorf("app protocol schema %q has no schema ID", ref.Name)
		}
		if obj.Name != "" && obj.Name != ref.Name {
			return fmt.Errorf("app protocol schema %q descriptor name is %q", ref.Name, obj.Name)
		}
		if previous, exists := schemaIDs[obj.SchemaID]; exists {
			return fmt.Errorf("app protocol schemas %q and %q share schema ID %d", previous, ref.Name, obj.SchemaID)
		}
		schemaNames[ref.Name] = struct{}{}
		schemaIDs[obj.SchemaID] = ref.Name
	}
	if len(reg.Schemas) != len(schemaNames) {
		return fmt.Errorf("app protocol contains undeclared schema descriptors")
	}
	requireSchema := func(kind, owner, name string) error {
		if _, ok := schemaNames[name]; !ok {
			return fmt.Errorf("app protocol %s %q references unknown schema %q", kind, owner, name)
		}
		return nil
	}
	seen := make(map[string]struct{}, len(d.Callables))
	for _, c := range d.Callables {
		if c.ID == "" || (c.RequestSchema == "") != (c.ResponseSchema == "") {
			return fmt.Errorf("app protocol callable is incomplete")
		}
		if _, ok := seen[c.ID]; ok {
			return fmt.Errorf("duplicate app protocol callable %q", c.ID)
		}
		if c.RequestSchema != "" {
			if err := requireSchema("callable request", c.ID, c.RequestSchema); err != nil {
				return err
			}
			if err := requireSchema("callable response", c.ID, c.ResponseSchema); err != nil {
				return err
			}
		}
		seen[c.ID] = struct{}{}
	}
	for _, event := range d.Events {
		if event.ID == "" {
			return fmt.Errorf("app protocol event is incomplete")
		}
		if event.PayloadSchema != "" {
			if err := requireSchema("event", event.ID, event.PayloadSchema); err != nil {
				return err
			}
		}
	}
	for _, projection := range d.Projections {
		if projection.ID == "" || projection.PayloadSchema == "" {
			return fmt.Errorf("app protocol projection is incomplete")
		}
		if err := requireSchema("projection", projection.ID, projection.PayloadSchema); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) RegisterAppProtocol(reg AppProtocolRegistration) error {
	if err := ValidateAppProtocol(reg); err != nil {
		return err
	}
	ids := make(map[string]uint64, len(reg.Descriptor.Schemas))
	for _, ref := range reg.Descriptor.Schemas {
		obj := reg.Schemas[ref.Name]
		if obj.SchemaID == 0 {
			return fmt.Errorf("app protocol schema %q has no schema ID", ref.Name)
		}
		ids[ref.Name] = obj.SchemaID
	}
	return m.RegisterExternal(reg.Descriptor.Namespace, ids)
}

func (m *Manager) RegisterAppManifest(manifest appgen.AppManifest) error {
	if manifest.Namespace == "" {
		return fmt.Errorf("app manifest namespace cannot be empty")
	}
	ids := make(map[string]uint64, len(manifest.Schemas))
	for _, ref := range manifest.Schemas {
		if ref.SchemaID == 0 {
			return fmt.Errorf("app manifest schema %q has no schema ID", ref.Name)
		}
		if ref.SchemaID <= 0 {
			return fmt.Errorf("app manifest schema %q has invalid schema ID", ref.Name)
		}
		ids[ref.Name] = uint64(ref.SchemaID)
	}
	if len(ids) == 0 {
		return nil
	}
	return m.RegisterExternal(manifest.Namespace, ids)
}

func (m *Manager) UnregisterAppProtocol(namespace string) error {
	return m.UnregisterExternal(namespace)
}
