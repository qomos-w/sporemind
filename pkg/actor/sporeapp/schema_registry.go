package sporeapp

import (
	"fmt"
	"reflect"

	"github.com/qomos-w/gospore/schema"
	sporesch "github.com/qomos-w/spore/schema"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/protocol"
)

// registerAppSchemas creates an app-scoped schema overlay from the manifest's
// declared schema refs. For each ref with SchemaID > 0, it checks the global
// type registry (populated at compile time by codegen init()). If the type
// exists, it's registered into the overlay Set with the correct wire ID and
// name. Types not found in the global registry are treated as opaque
// (BinaryCodec falls back to JSON for those).
//
// The overlay takes precedence over the existing reader; lookups that miss
// the overlay fall through to the ctx-provided schemas.
func (a *Actor) registerAppSchemas() (schema.Reader, error) {
	if len(a.Manifest.Schemas) == 0 {
		return a.schemas, nil
	}
	overlay, err := schema.New(overlayNamespace(a.Manifest.Namespace))
	if err != nil {
		return nil, err
	}
	objects, err := protocol.AppObjectDescriptors(a.SchemaDescriptors)
	if err != nil {
		return nil, err
	}
	for _, ref := range a.Manifest.Schemas {
		object, ok := objects[ref.Name]
		if !ok {
			return nil, fmt.Errorf("sporeapp: schema %q descriptor is missing", ref.Name)
		}
		td := sporesch.TypeDesc{Kind: object.Kind, Name: object.Name, TypeID: sporesch.TypeID(object.SchemaID)}
		if regErr := overlay.Register(object.SchemaID, ref.Name, td, object); regErr != nil {
			return nil, fmt.Errorf("sporeapp: register schema %q: %w", ref.Name, regErr)
		}
	}
	return &combinedSchemaReader{overlay: overlay, fallback: a.schemas, ns: a.Manifest.Namespace}, nil
}

// combinedSchemaReader checks the overlay first, then falls back to the
// parent reader for schemas not declared by this app.
type combinedSchemaReader struct {
	overlay  schema.Set
	fallback schema.Reader
	ns       string
}

func (c *combinedSchemaReader) Namespace() string { return c.ns }

func (c *combinedSchemaReader) Lookup(id uint64) (schema.Entry, bool) {
	if e, ok := c.overlay.Lookup(id); ok {
		return e, true
	}
	if c.fallback != nil {
		return c.fallback.Lookup(id)
	}
	return schema.Entry{}, false
}

func (c *combinedSchemaReader) LookupByName(name string) (schema.Entry, bool) {
	if e, ok := c.overlay.LookupByName(name); ok {
		return e, true
	}
	if c.fallback != nil {
		return c.fallback.LookupByName(name)
	}
	return schema.Entry{}, false
}

func (c *combinedSchemaReader) LookupSchema(id uint64) (sporesch.TypeDesc, bool) {
	if d, ok := c.overlay.LookupSchema(id); ok {
		return d, true
	}
	if c.fallback != nil {
		return c.fallback.LookupSchema(id)
	}
	return sporesch.TypeDesc{}, false
}

func (c *combinedSchemaReader) LookupObject(id uint64) (sporesch.ObjectDesc, bool) {
	if d, ok := c.overlay.LookupObject(id); ok {
		return d, true
	}
	if c.fallback != nil {
		return c.fallback.LookupObject(id)
	}
	return sporesch.ObjectDesc{}, false
}

func (c *combinedSchemaReader) Resolve(ns string, id uint64) (schema.Entry, bool) {
	if e, ok := c.overlay.Resolve(ns, id); ok {
		return e, true
	}
	if c.fallback != nil {
		return c.fallback.Resolve(ns, id)
	}
	return schema.Entry{}, false
}

func (c *combinedSchemaReader) ResolveByName(ns, name string) (schema.Entry, bool) {
	if e, ok := c.overlay.ResolveByName(ns, name); ok {
		return e, true
	}
	if c.fallback != nil {
		return c.fallback.ResolveByName(ns, name)
	}
	return schema.Entry{}, false
}

func (c *combinedSchemaReader) Namespaces() []string {
	nsMap := map[string]bool{}
	for _, ns := range c.overlay.Namespaces() {
		nsMap[ns] = true
	}
	if c.fallback != nil {
		for _, ns := range c.fallback.Namespaces() {
			nsMap[ns] = true
		}
	}
	result := make([]string, 0, len(nsMap))
	for ns := range nsMap {
		result = append(result, ns)
	}
	return result
}

func (c *combinedSchemaReader) Len() int {
	total := c.overlay.Len()
	if c.fallback != nil {
		total += c.fallback.Len()
	}
	return total
}

// buildDescriptors converts a reflect.Type into spore TypeDesc and ObjectDesc
// for schema.Set registration. For struct types, it extracts exported fields.
func buildDescriptors(typ reflect.Type, name string, id uint64) (sporesch.TypeDesc, sporesch.ObjectDesc) {
	td := sporesch.TypeDesc{
		Kind:   sporesch.TypeKindStruct,
		Name:   name,
		TypeID: sporesch.TypeID(id),
	}

	od := sporesch.ObjectDesc{
		Kind:     sporesch.TypeKindStruct,
		Name:     name,
		SchemaID: id,
	}

	// Extract exported fields for structural encoding.
	if typ.Kind() == reflect.Struct {
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			if !field.IsExported() {
				continue
			}
			jsonName := field.Name
			if tag := field.Tag.Get("json"); tag != "" && tag != "-" {
				if comma := indexOfByte(tag, ','); comma >= 0 {
					jsonName = tag[:comma]
				} else {
					jsonName = tag
				}
			}
			od.Fields = append(od.Fields, sporesch.FieldDesc{
				Name: jsonName,
				Type: sporesch.TypeDesc{Kind: sporesch.TypeKindScalar, Name: field.Type.String()},
			})
		}
	}

	return td, od
}

func indexOfByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// SchemaRegistryDebug returns a human-readable summary of registered app schemas.
func (a *Actor) SchemaRegistryDebug() string {
	if a.schemas == nil {
		return fmt.Sprintf("app %s: no schema reader", a.Manifest.ID)
	}
	resolvable := 0
	for _, ref := range a.Manifest.Schemas {
		if ref.SchemaID > 0 {
			if _, ok := sporesch.StructTypeByID(uint64(ref.SchemaID)); ok {
				resolvable++
			}
		}
	}
	return fmt.Sprintf("app %s: %d schema refs declared, %d resolvable in global registry", a.Manifest.ID, len(a.Manifest.Schemas), resolvable)
}

// Ensure gen import is used (AppSchemaRef referenced in registerAppSchemas).
var _ gen.AppSchemaRef

// overlayNamespace derives a schema.Set-valid namespace from an app manifest
// namespace. Manifest namespaces are dotted (e.g. "sporeapp.builtin.demo"),
// but schema.New requires `^[a-z][a-z0-9_]*$` — so the last segment is
// lower-cased and sanitised, falling back to "app" when nothing usable
// remains. The overlay namespace is only a lookup label; wire identity is
// carried by schema IDs.
func overlayNamespace(ns string) string {
	last := ns
	for i := len(ns) - 1; i >= 0; i-- {
		if ns[i] == '.' {
			last = ns[i+1:]
			break
		}
	}
	out := make([]byte, 0, len(last))
	for i := 0; i < len(last); i++ {
		c := last[i]
		switch {
		case c >= 'A' && c <= 'Z':
			out = append(out, c+('a'-'A'))
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 || out[0] < 'a' || out[0] > 'z' {
		return "app"
	}
	return string(out)
}
