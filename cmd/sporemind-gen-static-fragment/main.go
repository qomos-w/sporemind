// Command sporemind-gen-static-fragment parses every .spore schema file and
// assigns sequential ids from a filename base (e.g. agent.chat._300.spore →
// 300, 301, ...). Explicit @schema(N) annotations are ignored: the id is fully
// determined by the file's ._N base and the struct's declaration-order offset.
// It validates conflicts and writes a static schema fragment that becomes the
// single source of truth for internal schema IDs.
//
// Usage:
//
//	go run ./cmd/sporemind-gen-static-fragment -o gen/static_schema_fragment.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/qomos-w/spore/schema"
	"github.com/qomos-w/spore/script"
	"github.com/qomos-w/sporemind/pkg/protocol"
)

const (
	// MaxLocalSchemaID is the largest local schema ID a single namespace may use.
	// This leaves room for namespace high-bit offsets in Phase 2.
	MaxLocalSchemaID = protocol.LocalIDMask
)

type schemaFile struct {
	path    string
	objects []schema.ObjectDesc
}

func main() {
	var (
		outPath   = flag.String("o", "gen/static_schema_fragment.json", "output JSON path")
		schemaDir = flag.String("schemas", "schemas", "directory containing .spore files")
		ns        = flag.String("namespace", protocol.SystemNamespace, "owner namespace for internal schemas")
	)
	flag.Parse()

	if err := run(*outPath, *schemaDir, *ns); err != nil {
		fmt.Fprintln(os.Stderr, "sporemind-gen-static-fragment:", err)
		os.Exit(1)
	}
}

func run(outPath, schemaDir, ownerNamespace string) error {
	files, err := collectSchemaFiles(schemaDir)
	if err != nil {
		return err
	}

	entries, err := buildEntries(files, ownerNamespace)
	if err != nil {
		return err
	}

	fragment := protocol.StaticFragment{
		NamespaceOffsets: map[string]uint64{ownerNamespace: 0},
		Schemas:          entries,
	}

	data, err := json.MarshalIndent(fragment, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal fragment: %w", err)
	}
	data = append(data, '\n')

	if dir := filepath.Dir(outPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}

	fmt.Fprintf(os.Stderr, "sporemind-gen-static-fragment: wrote %s (%d schemas)\n", outPath, len(entries))
	return nil
}

func collectSchemaFiles(schemaDir string) ([]schemaFile, error) {
	entries, err := os.ReadDir(schemaDir)
	if err != nil {
		return nil, fmt.Errorf("read schemas dir: %w", err)
	}

	var files []schemaFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".spore") {
			continue
		}
		path := filepath.Join(schemaDir, e.Name())
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		objects, err := script.ParseObjects(string(src))
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		files = append(files, schemaFile{
			path:    path,
			objects: objects,
		})
	}

	// Sort for deterministic output order.
	sort.Slice(files, func(i, j int) bool {
		return files[i].path < files[j].path
	})
	return files, nil
}

func objectEqual(a, b schema.ObjectDesc) bool {
	// Compare structural shape only, ignoring source-file metadata like SchemaID.
	return a.Kind == b.Kind && a.Name == b.Name &&
		reflect.DeepEqual(a.Fields, b.Fields) &&
		a.Parent == b.Parent && a.IsOpen == b.IsOpen &&
		reflect.DeepEqual(a.Implements, b.Implements) &&
		reflect.DeepEqual(a.Methods, b.Methods)
}

// parseSchemaBaseID extracts an optional schema-id base from a filename such as
// "agent.chat._300.spore" → 300. Files without the "._<digits>" suffix return 0.
func parseSchemaBaseID(path string) (uint64, error) {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, ".spore")
	idx := strings.LastIndex(base, "._")
	if idx == -1 {
		return 0, nil
	}
	num := base[idx+2:]
	if num == "" {
		return 0, fmt.Errorf("%s: schema base id suffix is empty", path)
	}
	n, err := strconv.ParseUint(num, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid schema base id suffix %q: %w", path, num, err)
	}
	return uint64(n), nil
}

func buildEntries(files []schemaFile, ownerNamespace string) ([]protocol.StaticSchemaEntry, error) {
	// First pass: assign sequential schema IDs within each file that declares a
	// base ID via the `._N` filename suffix (e.g. `agent.chat._2000.spore`).
	// The ID is base + declaration-order offset. Any explicit @schema(N)
	// annotation parsed from the source is ignored so the ID is fully determined
	// by the filename and declaration order.
	for i := range files {
		f := &files[i]
		baseID, err := parseSchemaBaseID(f.path)
		if err != nil {
			return nil, err
		}
		if baseID == 0 {
			return nil, fmt.Errorf("%s: schema file must declare a base id via the ._N filename suffix", f.path)
		}
		var structIdx uint64
		for j := range f.objects {
			obj := &f.objects[j]
			if obj.Kind != schema.TypeKindStruct && obj.Kind != schema.TypeKindClass {
				continue
			}
			if obj.Name == "" {
				continue
			}
			obj.SchemaID = 0 // discard any explicit @schema(N)
			obj.SchemaID = baseID + structIdx
			structIdx++
		}
	}

	// Collect objects by name, merging identical duplicates across files.
	// Conflicting shapes with the same name are reported as errors.
	type objectSource struct {
		obj  schema.ObjectDesc
		path string
	}
	objectsByName := make(map[string]objectSource)
	for _, f := range files {
		for _, obj := range f.objects {
			if obj.Kind != schema.TypeKindStruct && obj.Kind != schema.TypeKindClass {
				continue
			}
			name := obj.Name
			if name == "" {
				continue
			}
			if prev, ok := objectsByName[name]; ok {
				if !objectEqual(prev.obj, obj) {
					return nil, fmt.Errorf("%s: schema conflict: struct %q already defined in %s with a different shape", f.path, name, prev.path)
				}
				if prev.obj.SchemaID != obj.SchemaID {
					return nil, fmt.Errorf("%s: schema conflict: struct %q has id %d but %s gives it id %d", f.path, name, prev.obj.SchemaID, prev.path, obj.SchemaID)
				}
				continue
			}
			objectsByName[name] = objectSource{obj: obj, path: f.path}
		}
	}

	// usedIDs tracks occupied IDs per namespace.
	usedIDs := make(map[string]map[uint64]string) // namespace -> id -> name

	var entries []protocol.StaticSchemaEntry

	for name, src := range objectsByName {
		obj := src.obj
		ns := ownerNamespace

		if obj.SchemaID == 0 {
			return nil, fmt.Errorf("%s: schema error: struct %q could not be assigned an id; ensure the file has a ._N base id suffix", src.path, name)
		}

		if err := validateLocalID(obj.SchemaID, name); err != nil {
			return nil, fmt.Errorf("%s: %w", src.path, err)
		}

		if usedIDs[ns] == nil {
			usedIDs[ns] = make(map[uint64]string)
		}
		if prevName, taken := usedIDs[ns][obj.SchemaID]; taken {
			return nil, fmt.Errorf("%s: schema conflict: local id %d is already used by %q in namespace %q", src.path, obj.SchemaID, prevName, ns)
		}
		usedIDs[ns][obj.SchemaID] = name

		entries = append(entries, protocol.StaticSchemaEntry{
			Namespace: ns,
			SchemaID:  obj.SchemaID,
			Name:      name,
			Object:    obj,
		})
	}

	// Sort entries for determinism.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Namespace != entries[j].Namespace {
			return entries[i].Namespace < entries[j].Namespace
		}
		if entries[i].SchemaID != entries[j].SchemaID {
			return entries[i].SchemaID < entries[j].SchemaID
		}
		return entries[i].Name < entries[j].Name
	})

	return entries, nil
}

func validateLocalID(id uint64, name string) error {
	if id == 0 {
		return fmt.Errorf("schema id for %q is 0, which is reserved", name)
	}
	if id <= protocol.BuiltinReserveEnd {
		return fmt.Errorf("local schemaId %d for %q is reserved for builtin scalars, use id >= %d", id, name, protocol.BuiltinReserveEnd+1)
	}
	if id > MaxLocalSchemaID {
		return fmt.Errorf("local schemaId %d for %q exceeds max local id %d", id, name, MaxLocalSchemaID)
	}
	return nil
}
