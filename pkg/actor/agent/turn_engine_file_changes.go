package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/textdiff"
)

// fileChangesFromResult 从文件类工具的原始返回中提取结构化变更信息。
// preReadContent/preReadOk 由 runOneCall 在 file_write / file_rm 前预读旧内容得到。
func (e *turnEngine) fileChangesFromResult(
	call pendingToolCall,
	rawResult any,
	preReadContent string,
	preReadOk bool,
) []domain.TurnFileChange {
	switch call.CallableID {
	case "project.edit":
		return fileChangesFromEditResult(call, rawResult)
	case "project.write":
		return fileChangesFromWriteResult(call, preReadContent, preReadOk)
	case "project.rm":
		return fileChangesFromRmResult(call, rawResult, preReadContent, preReadOk)
	}
	return nil
}

func fileChangesFromEditResult(call pendingToolCall, rawResult any) []domain.TurnFileChange {
	resp, ok := rawResult.(domain.FileSystemEditResp)
	if !ok {
		return nil
	}
	path := filePathFromInput(call.Input)
	if path == "" {
		return nil
	}
	additions, deletions := resp.Additions, resp.Deletions
	if len(resp.Hunks) == 0 && additions == 0 && deletions == 0 {
		// 无实际变更（如 old_string == new_string 已被过滤，但防御性处理）
		return nil
	}
	return []domain.TurnFileChange{{
		Path:        path,
		Additions:   additions,
		Deletions:   deletions,
		DiffContent: hunksToUnifiedDiff(resp.Hunks),
	}}
}

func fileChangesFromWriteResult(
	call pendingToolCall,
	preReadContent string,
	preReadOk bool,
) []domain.TurnFileChange {
	path, newContent := fileWriteArgs(call.Input)
	if path == "" {
		return nil
	}
	var oldContent string
	if preReadOk {
		oldContent = preReadContent
	} else {
		// 未预读到旧内容，按新文件处理。
		oldContent = ""
	}
	hunks, additions, deletions := textdiff.BuildHunks(normalizeLineEndings(oldContent), normalizeLineEndings(newContent))
	if len(hunks) == 0 {
		return nil
	}
	return []domain.TurnFileChange{{
		Path:        path,
		Additions:   int32(additions),
		Deletions:   int32(deletions),
		DiffContent: hunksToUnifiedDiff(hunks),
	}}
}

func fileChangesFromRmResult(
	call pendingToolCall,
	rawResult any,
	preReadContent string,
	preReadOk bool,
) []domain.TurnFileChange {
	path := filePathFromInput(call.Input)
	if path == "" {
		return nil
	}

	resp, ok := rawResult.(domain.FileSystemRmResp)
	if ok && len(resp.Removed) > 0 {
		// 递归删除可能返回多个文件；为每个被删除文件生成一个变更条目。
		out := make([]domain.TurnFileChange, 0, len(resp.Removed))
		for _, rel := range resp.Removed {
			var removedPath string
			if rel == filepath.Base(path) {
				// Single file removal: rel is just the basename.
				removedPath = path
			} else {
				// Recursive directory removal: rel is relative to the removed directory.
				removedPath = filepath.Join(path, rel)
			}
			var diffContent string
			var deletions int
			if preReadOk && len(resp.Removed) == 1 {
				// 单文件删除且预读成功：生成完整删除 diff。
				hunks, _, dels := textdiff.BuildHunks(normalizeLineEndings(preReadContent), "")
				diffContent = hunksToUnifiedDiff(hunks)
				deletions = dels
			}
			out = append(out, domain.TurnFileChange{
				Path:        removedPath,
				Additions:   0,
				Deletions:   int32(deletions),
				DiffContent: diffContent,
			})
		}
		return out
	}

	// 退化为单个路径条目（例如预览模式或空返回）。
	return []domain.TurnFileChange{{
		Path:        path,
		Additions:   0,
		Deletions:   0,
		DiffContent: "",
	}}
}

// readFileContent 通过 project.read 读取文件内容。
func (e *turnEngine) readFileContent(
	ctx actor.Context,
	planner actor.Planner,
	svcRef ref.Ref,
	path string,
) (string, bool) {
	if path == "" || planner == nil {
		return "", false
	}
	req := domain.FileSystemReadReq{Path: path}
	payload, err := json.Marshal(req)
	if err != nil {
		return "", false
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.ToolCallTimeout)
	defer cancel()
	result, err := planner.Call(callCtx, svcRef, "project.read", payload).Await()
	if err != nil {
		return "", false
	}
	resp, ok := result.(domain.FileSystemReadResp)
	if !ok {
		return "", false
	}
	return resp.Content, true
}

func filePathFromInput(inputJSON string) string {
	var req struct {
		Path string `json:"Path"`
	}
	if err := json.Unmarshal([]byte(inputJSON), &req); err != nil {
		return ""
	}
	return req.Path
}

func fileWriteArgs(inputJSON string) (string, string) {
	var req struct {
		Path    string `json:"Path"`
		Content string `json:"Content"`
	}
	if err := json.Unmarshal([]byte(inputJSON), &req); err != nil {
		return "", ""
	}
	return req.Path, req.Content
}

func normalizeLineEndings(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}

func hunksToUnifiedDiff(hunks []domain.FileSystemEditHunk) string {
	if len(hunks) == 0 {
		return ""
	}
	parts := make([]string, 0, len(hunks)*2)
	for _, h := range hunks {
		parts = append(parts, fmt.Sprintf("@@ -%d,%d +%d,%d @@", h.OldStart, h.OldLines, h.NewStart, h.NewLines))
		parts = append(parts, h.Lines...)
	}
	return strings.Join(parts, "\n")
}
