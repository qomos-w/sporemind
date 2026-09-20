package agent

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// profileEntry represents one stack-trace group in a pprof text profile.
// Each entry aggregates goroutines/allocations that share the same call stack.
type profileEntry struct {
	count  int      // goroutine count or object count
	bytes  int      // total bytes (heap profiles only)
	frames []string // symbolized function names (leaf first)
}

// key returns a stable identity for diffing: the joined function-name stack.
// Addresses are excluded because they vary between snapshots only on code
// reload (plugin hot-swap), which does not occur in this runtime.
func (e profileEntry) key() string {
	return strings.Join(e.frames, "\n")
}

// topFrame returns the first (leaf-most) function name, or "?" if unsymbolized.
func (e profileEntry) topFrame() string {
	if len(e.frames) == 0 {
		return "?"
	}
	return e.frames[0]
}

// label formats a human-readable description of the entry's magnitude.
func (e profileEntry) label() string {
	if e.bytes > 0 {
		return fmt.Sprintf("%d objs / %s", e.count, formatBytes(e.bytes))
	}
	return fmt.Sprintf("%d", e.count)
}

// formatBytes renders a byte count in human-readable form (B, KB, MB, GB).
func formatBytes(b int) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1fGB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(b)/(1<<10))
	default:
		return fmt.Sprintf("%dB", b)
	}
}

// parseProfileEntries parses the debug=1 text output of
// pprof.Lookup(name).WriteTo(w, 1) into structured entries. The format is:
//
//	<header line>
//	<empty>
//	<count>[: <bytes>] @ 0x<addr>
//	#\t0x<addr>\t<function+0xoffset>
//	#\t0x<addr>\t<function+0xoffset>
//	<empty>
//	<count> @ 0x<addr>
//	...
//
// The parser is intentionally tolerant: non-entry, non-frame lines are ignored.
func parseProfileEntries(text string) (header string, entries []profileEntry) {
	lines := strings.Split(text, "\n")
	var current *profileEntry

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// First non-empty line is the header.
		if header == "" && !isEntryLine(line) && !strings.HasPrefix(line, "#") {
			header = trimmed
			continue
		}

		// Entry line: "<count>[: <bytes>] @ 0x..." or "<count>: <bytes> [...] @ ..."
		if isEntryLine(line) {
			if current != nil {
				entries = append(entries, *current)
			}
			current = parseEntryLine(line)
			continue
		}

		// Stack frame line: "#\t0x<addr>\t<function+0xoffset>"
		if strings.HasPrefix(line, "#\t") && current != nil {
			if fn := extractFunctionName(line); fn != "" {
				current.frames = append(current.frames, fn)
			}
		}
	}
	if current != nil {
		entries = append(entries, *current)
	}
	return header, entries
}

// isEntryLine reports whether the line is a profile entry header, matching:
// "10 @ 0x1234", "100: 1024 [heap] @ 0x5678", "5: 2048 @ 0xabc"
func isEntryLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) == 0 || trimmed[0] < '0' || trimmed[0] > '9' {
		return false
	}
	return strings.Contains(trimmed, " @ ")
}

// parseEntryLine extracts count and bytes from an entry line. Examples:
//   - "10 @ 0x1234"           → count=10, bytes=0 (goroutine profile)
//   - "100: 1024 @ 0x5678"    → count=100, bytes=1024 (heap profile)
//   - "5: 2048 [heap] @ 0x9a" → count=5, bytes=2048
func parseEntryLine(line string) *profileEntry {
	entry := &profileEntry{}
	// Take everything before " @ "
	idx := strings.Index(line, " @ ")
	if idx < 0 {
		return entry
	}
	prefix := strings.TrimSpace(line[:idx])
	// Remove bracketed annotations like [heap], [GC]
	if bi := strings.Index(prefix, "["); bi >= 0 {
		prefix = strings.TrimSpace(prefix[:bi])
	}
	// Split on ":" for count:bytes, or take as single count
	if ci := strings.Index(prefix, ":"); ci >= 0 {
		entry.count, _ = strconv.Atoi(strings.TrimSpace(prefix[:ci]))
		entry.bytes, _ = strconv.Atoi(strings.TrimSpace(prefix[ci+1:]))
	} else {
		entry.count, _ = strconv.Atoi(prefix)
	}
	return entry
}

// extractFunctionName pulls the symbolized function name from a stack frame
// line. Input format: "#\t0x1234\tgithub.com/pkg/func+0x100"
// Returns empty string if the frame is unsymbolized.
func extractFunctionName(line string) string {
	parts := strings.Split(line, "\t")
	if len(parts) < 3 {
		return ""
	}
	fn := strings.TrimSpace(parts[len(parts)-1])
	if fn == "" || strings.HasPrefix(fn, "0x") {
		return ""
	}
	return fn
}

// totalEntryCount sums the counts of all entries.
func totalEntryCount(entries []profileEntry) int {
	total := 0
	for _, e := range entries {
		total += e.count
	}
	return total
}

// formatSummary formats the top N entries as readable text, sorted by count
// descending. Each entry shows its magnitude and abbreviated stack.
func formatSummary(header string, entries []profileEntry, topN int) string {
	if len(entries) == 0 {
		return fmt.Sprintf("%s\n(no entries)", header)
	}

	sorted := make([]profileEntry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].count != sorted[j].count {
			return sorted[i].count > sorted[j].count
		}
		return sorted[i].bytes > sorted[j].bytes
	})

	var sb strings.Builder
	total := totalEntryCount(entries)
	sb.WriteString(fmt.Sprintf("%s (total %d, %d unique stacks, showing top %d)\n\n",
		header, total, len(entries), minInt(topN, len(sorted))))

	for i, e := range sorted {
		if i >= topN {
			break
		}
		sb.WriteString(fmt.Sprintf("[%d] %s — %s\n", i+1, e.label(), e.topFrame()))
		if len(e.frames) > 1 {
			// Show up to 4 more frames for context
			maxFrames := minInt(len(e.frames), 5)
			for _, fn := range e.frames[1:maxFrames] {
				sb.WriteString(fmt.Sprintf("      ← %s\n", fn))
			}
			if len(e.frames) > 5 {
				sb.WriteString(fmt.Sprintf("      … (+%d more frames)\n", len(e.frames)-5))
			}
		}
	}
	if len(sorted) > topN {
		sb.WriteString(fmt.Sprintf("\n… (+%d more stacks; increase TopN to see more)\n", len(sorted)-topN))
	}
	return sb.String()
}

// diffProfileEntries compares two snapshots and returns entries that changed.
// An entry is "new" if it appears only in after; "growing" if its count
// increased; "shrinking" if its count decreased. Stable entries are omitted.
func diffProfileEntries(before, after []profileEntry) (added, grown, shrunk []profileEntry) {
	beforeMap := make(map[string]profileEntry, len(before))
	for _, e := range before {
		beforeMap[e.key()] = e
	}
	afterMap := make(map[string]profileEntry, len(after))
	for _, e := range after {
		afterMap[e.key()] = e
	}

	for k, ae := range afterMap {
		be, ok := beforeMap[k]
		if !ok {
			added = append(added, ae)
			continue
		}
		if ae.count > be.count {
			grown = append(grown, profileEntry{
				count:  ae.count - be.count,
				bytes:  ae.bytes - be.bytes,
				frames: ae.frames,
			})
		} else if ae.count < be.count {
			shrunk = append(shrunk, profileEntry{
				count:  be.count - ae.count,
				bytes:  be.bytes - be.bytes,
				frames: ae.frames,
			})
		}
	}
	return added, grown, shrunk
}

// formatDiff renders a diff result as readable text.
func formatDiff(profile string, before, after []profileEntry, added, grown, shrunk []profileEntry, topN int) string {
	var sb strings.Builder
	beforeTotal := totalEntryCount(before)
	afterTotal := totalEntryCount(after)
	delta := afterTotal - beforeTotal
	sign := "+"
	if delta < 0 {
		sign = ""
	}
	sb.WriteString(fmt.Sprintf("--- %s diff (before=%d, after=%d, %s%d) ---\n\n",
		profile, beforeTotal, afterTotal, sign, delta))

	if len(added) == 0 && len(grown) == 0 && len(shrunk) == 0 {
		sb.WriteString("No changes detected between snapshots.\n")
		return sb.String()
	}

	if len(added) > 0 {
		sortByCount(added)
		sb.WriteString(fmt.Sprintf("NEW (%d new stacks):\n", len(added)))
		for i, e := range added {
			if i >= topN {
				break
			}
			sb.WriteString(fmt.Sprintf("  +%s — %s\n", e.label(), e.topFrame()))
			writeFrames(&sb, e, "      ")
		}
		if len(added) > topN {
			sb.WriteString(fmt.Sprintf("  … (+%d more new)\n", len(added)-topN))
		}
		sb.WriteString("\n")
	}

	if len(grown) > 0 {
		sortByCount(grown)
		sb.WriteString(fmt.Sprintf("GROWING (%d stacks with increased count):\n", len(grown)))
		for i, e := range grown {
			if i >= topN {
				break
			}
			sb.WriteString(fmt.Sprintf("  +%s — %s\n", e.label(), e.topFrame()))
			writeFrames(&sb, e, "      ")
		}
		if len(grown) > topN {
			sb.WriteString(fmt.Sprintf("  … (+%d more growing)\n", len(grown)-topN))
		}
		sb.WriteString("\n")
	}

	if len(shrunk) > 0 {
		sortByCount(shrunk)
		sb.WriteString(fmt.Sprintf("SHRINKING (%d stacks with decreased count):\n", len(shrunk)))
		for i, e := range shrunk {
			if i >= topN {
				break
			}
			sb.WriteString(fmt.Sprintf("  -%s — %s\n", e.label(), e.topFrame()))
			writeFrames(&sb, e, "      ")
		}
		if len(shrunk) > topN {
			sb.WriteString(fmt.Sprintf("  … (+%d more shrinking)\n", len(shrunk)-topN))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// writeFrames writes up to 5 function frames with the given indent.
func writeFrames(sb *strings.Builder, e profileEntry, indent string) {
	maxFrames := minInt(len(e.frames), 5)
	for _, fn := range e.frames[:maxFrames] {
		sb.WriteString(fmt.Sprintf("%s%s\n", indent, fn))
	}
	if len(e.frames) > 5 {
		sb.WriteString(fmt.Sprintf("%s… (+%d more frames)\n", indent, len(e.frames)-5))
	}
}

// sortByCount sorts entries by count descending in place.
func sortByCount(entries []profileEntry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].count > entries[j].count
	})
}

// minInt returns the smaller of two ints.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
