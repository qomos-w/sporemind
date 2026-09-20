package converter

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func init() {
	Register(reflect.TypeOf(domain.FileSystemGlobResp{}), globConverter)
	Register(reflect.TypeOf(domain.FileSystemGrepResp{}), grepConverter)
	Register(reflect.TypeOf(domain.FileSystemReadResp{}), readConverter)
	Register(reflect.TypeOf(domain.FileSystemRmResp{}), rmConverter)
}

// noMatchesPlaceholder is the human-readable text emitted when a glob or grep
// returns zero results. It is shown in the LLM-facing tool result and must be
// parsed consistently by the frontend.
const noMatchesPlaceholder = "(no matches)"

// maxGrepLineChars bounds how many characters of a matched line are rendered
// in the LLM-facing output. Matches exceeding this (e.g. minified JS, base64
// blobs, packed JSON) are truncated so a single long line cannot blow up the
// tool-result token budget.
const maxGrepLineChars = 500

// truncateLine caps a matched line at maxGrepLineChars runes, appending a
// marker when content was dropped.
func truncateLine(s string) string {
	if len(s) <= maxGrepLineChars {
		return s
	}
	r := []rune(s)
	if len(r) <= maxGrepLineChars {
		return s
	}
	return fmt.Sprintf("%s... (+%d more chars)", string(r[:maxGrepLineChars]), len(r)-maxGrepLineChars)
}

// globConverter formats glob results as a shell-like file list.
func globConverter(result any) (string, error) {
	resp, ok := result.(domain.FileSystemGlobResp)
	if !ok {
		return "", fmt.Errorf("globConverter: expected FileSystemGlobResp, got %T", result)
	}
	if len(resp.Files) == 0 {
		return withConverterNote(noMatchesPlaceholder, resp.Note), nil
	}
	var sb strings.Builder
	for _, f := range resp.Files {
		sb.WriteString(f)
		sb.WriteByte('\n')
	}
	if resp.Truncated {
		sb.WriteString(fmt.Sprintf("[truncated: showing %d of more total, refine pattern]\n", len(resp.Files)))
	}
	sb.WriteString(fmt.Sprintf("(%d files)", len(resp.Files)))
	return withConverterNote(sb.String(), resp.Note), nil
}

// withConverterNote appends an advisory note (e.g. worktree-outside reads) to
// a rendered tool result.
func withConverterNote(s, note string) string {
	if note == "" {
		return s
	}
	return s + "\n\n" + note
}

// grepConverter formats grep results based on OutputMode.
func grepConverter(result any) (string, error) {
	resp, ok := result.(domain.FileSystemGrepResp)
	if !ok {
		return "", fmt.Errorf("grepConverter: expected FileSystemGrepResp, got %T", result)
	}

	mode := resp.OutputMode
	if mode == "" {
		mode = "files" // default
	}

	switch mode {
	case "files":
		return withConverterNote(formatGrepFiles(resp), resp.Note), nil
	case "count":
		return withConverterNote(formatGrepCount(resp), resp.Note), nil
	default: // "content" and any other
		return withConverterNote(formatGrepContent(resp), resp.Note), nil
	}
}

func formatGrepFiles(resp domain.FileSystemGrepResp) string {
	files := resp.Files
	if len(files) == 0 {
		return noMatchesPlaceholder
	}
	sort.Strings(files)
	var sb strings.Builder
	for _, f := range files {
		sb.WriteString(f)
		sb.WriteByte('\n')
	}
	if resp.Truncated {
		sb.WriteString(fmt.Sprintf("[truncated: showing %d of %d total, refine search]\n", len(files), resp.NumMatches))
	}
	sb.WriteString(fmt.Sprintf("(%d files)", len(files)))
	return sb.String()
}

func formatGrepCount(resp domain.FileSystemGrepResp) string {
	if len(resp.Counts) == 0 {
		return noMatchesPlaceholder
	}
	sort.Slice(resp.Counts, func(i, j int) bool {
		return resp.Counts[i].File < resp.Counts[j].File
	})
	var sb strings.Builder
	var total int32
	for _, c := range resp.Counts {
		fmt.Fprintf(&sb, "%s: %d matches\n", c.File, c.Count)
		total += c.Count
	}
	if resp.Truncated {
		sb.WriteString(fmt.Sprintf("[truncated: showing counts for %d of %d total matches, refine search]\n", len(resp.Counts), resp.NumMatches))
	}
	fmt.Fprintf(&sb, "(%d total matches in %d files)", total, len(resp.Counts))
	return sb.String()
}

func formatGrepContent(resp domain.FileSystemGrepResp) string {
	if len(resp.Matches) == 0 {
		return noMatchesPlaceholder
	}
	var sb strings.Builder
	// Group by file for readability.
	byFile := make(map[string][]domain.FileSystemGrepMatch)
	var fileOrder []string
	for _, m := range resp.Matches {
		if _, ok := byFile[m.File]; !ok {
			fileOrder = append(fileOrder, m.File)
		}
		byFile[m.File] = append(byFile[m.File], m)
	}

	for i, file := range fileOrder {
		if i > 0 {
			sb.WriteByte('\n')
		}
		for _, m := range byFile[file] {
			fmt.Fprintf(&sb, "%s:%d:%s\n", m.File, m.Line, truncateLine(m.Content))
		}
	}
	if resp.Truncated {
		sb.WriteString(fmt.Sprintf("[truncated: showing %d of %d total matches, refine search]\n", len(resp.Matches), resp.NumMatches))
	}
	fmt.Fprintf(&sb, "(%d matches in %d files)", resp.NumMatches, len(fileOrder))
	return sb.String()
}

// readConverter formats read results with line numbers.
func readConverter(result any) (string, error) {
	resp, ok := result.(domain.FileSystemReadResp)
	if !ok {
		return "", fmt.Errorf("readConverter: expected FileSystemReadResp, got %T", result)
	}
	lines := strings.Split(resp.Content, "\n")
	// Remove trailing empty line caused by trailing newline.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return "(empty file)", nil
	}
	var sb strings.Builder
	startLine := resp.StartLine
	if startLine <= 0 {
		startLine = 1
	}
	for i, line := range lines {
		lineNum := int(startLine) + i
		fmt.Fprintf(&sb, "%5d│%s\n", lineNum, line)
	}
	// Truncation is implicit if NumLines < TotalLines and Offset+Limit reached end.
	if resp.NumLines < resp.TotalLines {
		fmt.Fprintf(&sb, "[truncated: showing %d of %d total lines, use offset=%d to continue]\n",
			resp.NumLines, resp.TotalLines, startLine+resp.NumLines)
	}
	return withConverterNote(sb.String(), resp.Note), nil
}

// rmConverter formats rm results in POSIX style.
func rmConverter(result any) (string, error) {
	resp, ok := result.(domain.FileSystemRmResp)
	if !ok {
		return "", fmt.Errorf("rmConverter: expected FileSystemRmResp, got %T", result)
	}
	if resp.Preview {
		return formatRmPreview(resp), nil
	}
	if len(resp.Removed) == 0 {
		return "(no files removed)", nil
	}
	if len(resp.Removed) == 1 {
		return fmt.Sprintf("removed '%s'", resp.Removed[0]), nil
	}
	return fmt.Sprintf("removed '%s' (%d files)", resp.Removed[0], resp.Count), nil
}

func formatRmPreview(resp domain.FileSystemRmResp) string {
	if len(resp.Removed) == 0 {
		return "(no files to remove)"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d files would be removed:\n", resp.Count)
	const maxPreview = 20
	for i, p := range resp.Removed {
		if i >= maxPreview {
			fmt.Fprintf(&sb, "... (showing %d of %d entries)\n", maxPreview, len(resp.Removed))
			break
		}
		sb.WriteString("  ")
		sb.WriteString(p)
		sb.WriteByte('\n')
	}
	sb.WriteString("Run with --confirm to proceed.")
	return sb.String()
}
