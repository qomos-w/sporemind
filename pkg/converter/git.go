package converter

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func init() {
	Register(reflect.TypeOf(domain.ProjectGitStatusResp{}), projectGitStatusConverter)
	Register(reflect.TypeOf(domain.ProjectGitDiffResp{}), projectGitDiffConverter)
	Register(reflect.TypeOf(domain.WorkspaceGitStatusResp{}), workspaceGitStatusConverter)
	Register(reflect.TypeOf(domain.WorkspaceGitDiffResp{}), workspaceGitDiffConverter)
	Register(reflect.TypeOf(gen.ProjectReviewChangesetSummaryResp{}), reviewChangesetSummaryConverter)
	Register(reflect.TypeOf(gen.ProjectReviewFileContentResp{}), reviewFileContentConverter)
}

// gitFileRow is the normalized, source-agnostic file entry shared by the
// project and workspace status converters.
type gitFileRow struct {
	staging  string
	worktree string
	path     string
}

// formatGitDiff renders a unified diff as plain text. The []string already
// holds standard unified-diff lines; we only strip the JSON array wrapper and
// join them so the model reads a real diff instead of an escaped string list.
func formatGitDiff(lines []string) string {
	if len(lines) == 0 {
		return "(no changes)"
	}
	return strings.Join(lines, "\n")
}

func projectGitDiffConverter(result any) (string, error) {
	resp, ok := result.(domain.ProjectGitDiffResp)
	if !ok {
		return "", fmt.Errorf("projectGitDiffConverter: expected ProjectGitDiffResp, got %T", result)
	}
	return formatGitDiff(resp.Diff), nil
}

func workspaceGitDiffConverter(result any) (string, error) {
	resp, ok := result.(domain.WorkspaceGitDiffResp)
	if !ok {
		return "", fmt.Errorf("workspaceGitDiffConverter: expected WorkspaceGitDiffResp, got %T", result)
	}
	return formatGitDiff(resp.Diff), nil
}

// formatGitStatus renders a compact, sorted status summary. Only branch,
// IsGit and the per-file status are surfaced; redundant raw porcelain and
// unrelated fields (recent log, user name) are dropped.
func formatGitStatus(branch string, rows []gitFileRow, isGit bool) string {
	if !isGit {
		return "(not a git repository)"
	}
	var sb strings.Builder
	if branch != "" {
		fmt.Fprintf(&sb, "On branch %s\n", branch)
	}
	if len(rows) == 0 {
		sb.WriteString("\n(no uncommitted changes)\n")
		return sb.String()
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].path < rows[j].path })
	sb.WriteByte('\n')
	for _, r := range rows {
		fmt.Fprintf(&sb, "%s%s %s  (%s)\n", r.staging, r.worktree, r.path, gitFileLabel(r.staging, r.worktree))
	}
	fmt.Fprintf(&sb, "\n(%d files changed)\n", len(rows))
	return sb.String()
}

func projectGitStatusConverter(result any) (string, error) {
	resp, ok := result.(domain.ProjectGitStatusResp)
	if !ok {
		return "", fmt.Errorf("projectGitStatusConverter: expected ProjectGitStatusResp, got %T", result)
	}
	rows := make([]gitFileRow, 0, len(resp.Files))
	for _, f := range resp.Files {
		rows = append(rows, gitFileRow{staging: f.Staging, worktree: f.Worktree, path: f.Path})
	}
	return formatGitStatus(resp.Branch, rows, resp.IsGit), nil
}

func workspaceGitStatusConverter(result any) (string, error) {
	resp, ok := result.(domain.WorkspaceGitStatusResp)
	if !ok {
		return "", fmt.Errorf("workspaceGitStatusConverter: expected WorkspaceGitStatusResp, got %T", result)
	}
	rows := make([]gitFileRow, 0, len(resp.Files))
	for _, f := range resp.Files {
		rows = append(rows, gitFileRow{staging: f.Staging, worktree: f.Worktree, path: f.Path})
	}
	return formatGitStatus(resp.Branch, rows, resp.IsGit), nil
}

// gitStatusWord maps a single porcelain status code (X or Y column) to a word.
// A blank code (unmodified) yields "".
func gitStatusWord(code string) string {
	switch code {
	case " ", "":
		return ""
	case "M":
		return "modified"
	case "A":
		return "added"
	case "D":
		return "deleted"
	case "R":
		return "renamed"
	case "C":
		return "copied"
	case "T":
		return "type-changed"
	case "U":
		return "unmerged"
	case "?":
		return "untracked"
	case "!":
		return "ignored"
	default:
		return code
	}
}

// gitFileLabel builds a human-readable description from the staged (X) and
// worktree (Y) status codes of one file.
func gitFileLabel(staging, worktree string) string {
	if staging == "?" || worktree == "?" {
		return "untracked"
	}
	sw, ww := gitStatusWord(staging), gitStatusWord(worktree)
	var parts []string
	if sw != "" {
		parts = append(parts, sw+", staged")
	}
	if ww != "" {
		parts = append(parts, ww+", unstaged")
	}
	if len(parts) == 0 {
		return "modified"
	}
	return strings.Join(parts, "; ")
}

// ── Review changeset converters ──

func reviewChangesetSummaryConverter(result any) (string, error) {
	resp, ok := result.(gen.ProjectReviewChangesetSummaryResp)
	if !ok {
		return "", fmt.Errorf("reviewChangesetSummaryConverter: expected ProjectReviewChangesetSummaryResp, got %T", result)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Review Changeset for Agent %s\n", resp.AgentActorID)
	if resp.Status != "" && resp.Status != "ready" {
		fmt.Fprintf(&sb, "Status: %s\n", resp.Status)
		if resp.ErrorMsg != "" {
			fmt.Fprintf(&sb, "Error: %s\n", resp.ErrorMsg)
		}
	}
	fmt.Fprintf(&sb, "Branch: %s  Head: %s  Baseline: %s\n", resp.Branch, resp.Head, resp.Baseline)
	if resp.IsDirty {
		sb.WriteString("Working tree: DIRTY (uncommitted changes present)\n")
	}
	sb.WriteString("\n## Commits\n")
	if len(resp.Commits) == 0 {
		sb.WriteString("(none)\n")
	}
	for _, c := range resp.Commits {
		fmt.Fprintf(&sb, "  %s %s\n", c.Short, c.Message)
	}
	sb.WriteString("\n## Tracked Files Changed\n")
	if len(resp.Files) == 0 {
		sb.WriteString("(none)\n")
	}
	for _, f := range resp.Files {
		flags := ""
		if f.IsBinary {
			flags += " [binary]"
		}
		if f.IsGenerated {
			flags += " [generated]"
		}
		oldPath := ""
		if f.OldPath != "" {
			oldPath = fmt.Sprintf(" (was %s)", f.OldPath)
		}
		fmt.Fprintf(&sb, "  %-12s %s%s%s (%d diff lines)\n", f.Status, f.Path, oldPath, flags, f.DiffLines)
	}
	sb.WriteString("\n## Untracked Files\n")
	if len(resp.UntrackedFiles) == 0 {
		sb.WriteString("(none)\n")
	}
	for _, f := range resp.UntrackedFiles {
		flags := ""
		if f.IsBinary {
			flags += " [binary]"
		}
		if !f.IsText {
			flags += " [not displayable]"
		}
		fmt.Fprintf(&sb, "  %s%s (%d bytes)\n", f.Path, flags, f.Size)
	}
	sb.WriteString("\n## Stats\n")
	fmt.Fprintf(&sb, "  Tracked changed: %d, Untracked: %d\n", resp.Stats.TrackedChanged, resp.Stats.Untracked)
	fmt.Fprintf(&sb, "  +%d / -%d lines (%d total diff lines)\n", resp.Stats.AddedLines, resp.Stats.DeletedLines, resp.Stats.TotalDiffLines)
	sb.WriteString("\n## Test Result\n")
	tr := resp.TestResult
	if tr.Skipped {
		sb.WriteString("  (skipped — no test command)\n")
	} else {
		status := "PASS"
		if !tr.Success {
			status = "FAIL"
		}
		fmt.Fprintf(&sb, "  %s — %s (exit %d, %s)\n", status, tr.Command, tr.ExitCode, tr.Duration)
		if tr.Output != "" {
			sb.WriteString("  --- output (tail) ---\n")
			for _, line := range strings.Split(strings.TrimSpace(tr.Output), "\n") {
				fmt.Fprintf(&sb, "  %s\n", line)
			}
		}
	}
	return sb.String(), nil
}

func reviewFileContentConverter(result any) (string, error) {
	resp, ok := result.(gen.ProjectReviewFileContentResp)
	if !ok {
		return "", fmt.Errorf("reviewFileContentConverter: expected ProjectReviewFileContentResp, got %T", result)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "File: %s (%s)", resp.FilePath, resp.Status)
	if resp.IsBinary {
		sb.WriteString(" [binary]")
	}
	if resp.IsGenerated {
		sb.WriteString(" [generated]")
	}
	fmt.Fprintf(&sb, " — lines %d-%d of %d", resp.StartLine, resp.StartLine+resp.NumLines-1, resp.TotalLines)
	if resp.Truncated {
		sb.WriteString(" (truncated)")
	}
	sb.WriteString("\n\n")
	sb.WriteString(resp.Content)
	return sb.String(), nil
}
