// Command sporemind-renumber-schemas reassigns the `._N` base ids of every
// `*.spore` file so each module (the filename stem with any `.partN` suffix
// stripped) owns one contiguous id segment sized to its struct count plus a
// proportional gap. Because all codegen derives ids purely from the `._N`
// filename suffix and declaration order, a run + `make gen` regenerates every
// downstream artifact consistently.
//
// By default it prints a dry-run plan; pass -w to apply renames.
package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/qomos-w/spore/schema"
	"github.com/qomos-w/spore/script"
	"github.com/qomos-w/sporemind/pkg/protocol"
)

const (
	maxLocalSchemaID = protocol.LocalIDMask
	startID          = uint64(protocol.BuiltinReserveEnd + 1)
	minGap           = uint64(32)
	align            = uint64(16)
)

var partRe = regexp.MustCompile(`\.part(\d+)$`)

type fileEntry struct {
	path    string
	dir     string
	name    string
	stem    string // name minus ".spore"
	module  string
	part    int
	baseID  uint64
	count   int
	objects []schema.ObjectDesc
}

type module struct {
	key     string
	files   []*fileEntry
	minBase uint64
	count   int
}

func main() {
	var (
		dir   = flag.String("schemas", "schemas", "directory containing .spore files")
		write = flag.Bool("w", false, "apply renames (default: dry run)")
	)
	flag.Parse()

	if err := run(*dir, *write); err != nil {
		fmt.Fprintln(os.Stderr, "sporemind-renumber-schemas:", err)
		os.Exit(1)
	}
}

func run(schemaDir string, write bool) error {
	entries, err := collect(schemaDir)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return fmt.Errorf("no .spore files in %s", schemaDir)
	}

	mods := group(entries)
	sort.Slice(mods, func(i, j int) bool {
		if mods[i].minBase != mods[j].minBase {
			return mods[i].minBase < mods[j].minBase
		}
		return mods[i].key < mods[j].key
	})

	cursor := startID
	var renames []renameOp
	fmt.Fprintf(os.Stderr, "%-34s %6s %5s %10s -> %-10s\n", "module", "count", "gap", "oldStart", "newStart")
	for _, m := range mods {
		oldStart := m.files[0].baseID
		start := alignUp(cursor)
		gap := gapFor(m.count)
		segNext := start + uint64(m.count) + gap
		if segNext-1 > maxLocalSchemaID {
			return fmt.Errorf("module %s would exceed max local schema id %d", m.key, maxLocalSchemaID)
		}
		cursor = segNext

		off := uint64(0)
		for _, f := range m.files {
			newBase := start + off
			off += uint64(f.count)
			if newBase != f.baseID {
				renames = append(renames, renameOp{f: f, newBase: newBase})
			}
			fmt.Fprintf(os.Stderr, "  %-38s -> %-40s (%d)\n",
				f.name, newFileName(f.stem, newBase), f.count)
		}
		fmt.Fprintf(os.Stderr, "%-34s %6d %5d %10d -> %-10d\n\n",
			m.key, m.count, gap, oldStart, start)
	}

	fmt.Fprintf(os.Stderr, "sporemind-renumber-schemas: %d file(s) need rename (%d total module(s), last id %d)\n",
		len(renames), len(mods), cursor-1)

	if !write {
		if len(renames) > 0 {
			fmt.Fprintln(os.Stderr, "dry run; pass -w to apply")
		}
		return nil
	}

	if err := apply(renames); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "sporemind-renumber-schemas: applied %d renames; run 'make gen' to regenerate\n", len(renames))
	return nil
}

type renameOp struct {
	f       *fileEntry
	newBase uint64
}

func collect(schemaDir string) ([]*fileEntry, error) {
	dirEntries, err := os.ReadDir(schemaDir)
	if err != nil {
		return nil, fmt.Errorf("read schemas dir: %w", err)
	}
	var out []*fileEntry
	for _, e := range dirEntries {
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
		base, err := parseBaseID(e.Name())
		if err != nil {
			return nil, err
		}
		if base == 0 {
			return nil, fmt.Errorf("%s: schema file must declare a base id via the ._N filename suffix", path)
		}
		mod, part := splitModule(e.Name())
		out = append(out, &fileEntry{
			path:    path,
			dir:     schemaDir,
			name:    e.Name(),
			stem:    strings.TrimSuffix(e.Name(), ".spore"),
			module:  mod,
			part:    part,
			baseID:  base,
			count:   countIDObjects(objects),
			objects: objects,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

func group(files []*fileEntry) []*module {
	byKey := make(map[string]*module)
	var order []string
	for _, f := range files {
		m, ok := byKey[f.module]
		if !ok {
			m = &module{key: f.module}
			byKey[f.module] = m
			order = append(order, f.module)
		}
		m.files = append(m.files, f)
		m.count += f.count
		if f.baseID < m.minBase || m.minBase == 0 {
			m.minBase = f.baseID
		}
	}
	mods := make([]*module, 0, len(order))
	for _, k := range order {
		m := byKey[k]
		sort.Slice(m.files, func(i, j int) bool {
			fi, fj := m.files[i], m.files[j]
			if fi.part != fj.part {
				return fi.part < fj.part
			}
			return fi.baseID < fj.baseID
		})
		mods = append(mods, m)
	}
	return mods
}

// parseBaseID mirrors parseSchemaBaseID in sporemind-gen-static-fragment.
func parseBaseID(name string) (uint64, error) {
	stem := strings.TrimSuffix(name, ".spore")
	idx := strings.LastIndex(stem, "._")
	if idx == -1 {
		return 0, nil
	}
	num := stem[idx+2:]
	if num == "" {
		return 0, fmt.Errorf("%s: schema base id suffix is empty", name)
	}
	n, err := strconv.ParseUint(num, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid schema base id suffix %q: %w", name, num, err)
	}
	return n, nil
}

// splitModule returns the module key (stem minus any trailing .partN) and the
// part number (0 when the file is not a part file).
func splitModule(name string) (string, int) {
	stem := strings.TrimSuffix(name, ".spore")
	idx := strings.LastIndex(stem, "._")
	prefix := stem
	if idx != -1 {
		prefix = stem[:idx]
	}
	if m := partRe.FindStringSubmatch(prefix); m != nil {
		part, _ := strconv.Atoi(m[1])
		return strings.TrimSuffix(prefix, m[0]), part
	}
	return prefix, 0
}

func countIDObjects(objects []schema.ObjectDesc) int {
	var n int
	for _, o := range objects {
		if o.Name == "" {
			continue
		}
		if o.Kind == schema.TypeKindStruct || o.Kind == schema.TypeKindClass {
			n++
		}
	}
	return n
}

func newFileName(stem string, base uint64) string {
	idx := strings.LastIndex(stem, "._")
	prefix := stem
	if idx != -1 {
		prefix = stem[:idx]
	}
	return fmt.Sprintf("%s._%d.spore", prefix, base)
}

func alignUp(n uint64) uint64 {
	return (n + align - 1) &^ (align - 1)
}

func gapFor(count int) uint64 {
	half := uint64(math.Ceil(float64(count) / 2))
	g := alignUp(half)
	if g < minGap {
		g = minGap
	}
	return g
}

func apply(renames []renameOp) error {
	// Two-phase: move all changed files aside, then to final names, so no
	// intermediate collision occurs (e.g. on case-insensitive filesystems).
	tmp := make([]string, len(renames))
	for i, r := range renames {
		staged := filepath.Join(r.f.dir, "."+r.f.name+".renumber-staged")
		if err := os.Rename(r.f.path, staged); err != nil {
			return fmt.Errorf("stage %s: %w", r.f.path, err)
		}
		tmp[i] = staged
	}
	for i, r := range renames {
		final := filepath.Join(r.f.dir, newFileName(r.f.stem, r.newBase))
		if err := os.Rename(tmp[i], final); err != nil {
			return fmt.Errorf("rename to %s: %w", final, err)
		}
		r.f.path = final
		if err := rewriteAnnotations(r.f, r.newBase); err != nil {
			return err
		}
	}
	return nil
}

// rewriteAnnotations keeps any explicit @schema(N) annotations truthful after
// renumbering. The codegen ignores them, but stale numbers mislead readers.
func rewriteAnnotations(f *fileEntry, newBase uint64) error {
	var off uint64
	mutated := false
	src, err := os.ReadFile(f.path)
	if err != nil {
		return fmt.Errorf("read %s: %w", f.path, err)
	}
	content := string(src)
	for _, o := range f.objects {
		if o.Name == "" {
			continue
		}
		if o.Kind != schema.TypeKindStruct && o.Kind != schema.TypeKindClass {
			continue
		}
		newID := newBase + off
		off++
		if o.SchemaID != 0 && o.SchemaID != newID {
			oldTag := fmt.Sprintf("@schema(%d)", o.SchemaID)
			newTag := fmt.Sprintf("@schema(%d)", newID)
			before := content
			content = strings.Replace(content, oldTag, newTag, 1)
			if content != before {
				mutated = true
			}
		}
	}
	if mutated {
		if err := os.WriteFile(f.path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", f.path, err)
		}
	}
	return nil
}
