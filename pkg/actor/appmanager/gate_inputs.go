package appmanager

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/codegen"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// projectCaller is a small wrapper around a project actor reference that makes
// the gate input collection code reusable between dev_gate and register_project.
type projectCaller struct {
	ctx        actor.Context
	planner    actor.Planner
	projectRef ref.Ref
}

func newProjectCaller(ctx actor.Context, projectRef ref.Ref) *projectCaller {
	return &projectCaller{ctx: ctx, planner: ctx.Planner(), projectRef: projectRef}
}

func (c *projectCaller) call(name string, payload any) (any, error) {
	return c.planner.Call(c.ctx.Lifecycle(), c.projectRef, name, payload).Await()
}

func (c *projectCaller) readText(path string) (string, error) {
	// Byte-exact path: project.read normalizes CRLF AND appends a trailing
	// newline when the file has none, so hashing its output drifts from the
	// raw bytes dev_generate hashed (no-trailing-newline .appdef) and also
	// silently truncates >2000 lines (implicitLimit). read_base64 returns
	// the exact file bytes; normalization is applied by the caller.
	v, err := c.call("project.read_base64", gen.FileSystemReadBase64Req{Path: path})
	if err != nil {
		return "", err
	}
	r, ok := v.(gen.FileSystemReadBase64Resp)
	if !ok {
		return "", fmt.Errorf("unexpected project.read_base64 response type %T", v)
	}
	data, err := base64.StdEncoding.DecodeString(r.Content)
	if err != nil {
		return "", fmt.Errorf("decode base64: %w", err)
	}
	return string(data), nil
}

// resolveSingleAppDef locates the project's .appdef file. When exactly one
// exists its content and SHA-256 hash are returned. Multiple .appdef files are
// treated as an error because codegen overwrites app.manifest.json per file and
// the gate cannot determine which file is authoritative.
func (c *projectCaller) resolveSingleAppDef(root string) (string, string, error) {
	listValue, err := c.call("project.list", gen.FileSystemListReq{Path: root, Depth: 1, NoIgnore: true})
	if err != nil {
		return "", "", fmt.Errorf("list project root: %w", err)
	}
	list, ok := listValue.(string)
	if !ok {
		return "", "", fmt.Errorf("unexpected project.list response type %T", listValue)
	}
	var appdefFiles []string
	for _, item := range parseListLines(list) {
		if !item.isDir && strings.HasSuffix(item.name, ".appdef") {
			appdefFiles = append(appdefFiles, item.name)
		}
	}
	sort.Strings(appdefFiles)
	switch len(appdefFiles) {
	case 0:
		return "", "", fmt.Errorf("no .appdef file found in project root")
	case 1:
		path := root + "/" + appdefFiles[0]
		content, err := c.readText(path)
		if err != nil {
			return "", "", fmt.Errorf("read %s: %w", appdefFiles[0], err)
		}
		// Hash the normalized form (CRLF → LF), matching dev_generate's
		// contentHash(normalizeLineEndings(...)) so raw-byte and gate paths
		// agree regardless of the file's line endings.
		return content, hashString(codegen.NormalizeLineEndings(content)), nil
	default:
		return "", "", fmt.Errorf("multiple .appdef files found: %s; gate requires a single authoritative .appdef", strings.Join(appdefFiles, ", "))
	}
}

// resolveSingleAppDefOptional is like resolveSingleAppDef but returns
// (content, hash, true) on success or ("", "", false) when no .appdef file is
// found (legacy project path). Multiple .appdef files are still an error.
func resolveSingleAppDefOptional(c *projectCaller, root string) (content, hash string, ok bool) {
	content, hash, err := c.resolveSingleAppDef(root)
	if err == nil {
		return content, hash, true
	}
	if strings.Contains(err.Error(), "no .appdef file found") {
		return "", "", false
	}
	return "", "", false
}

// collectGoSourceFiles recursively reads every .go file under root and returns
// a map of relative path -> content. It is used by the stub-filling gate.
func (c *projectCaller) collectGoSourceFiles(root string) (map[string]string, error) {
	listValue, err := c.call("project.list", gen.FileSystemListReq{Path: root, Depth: -1, NoIgnore: true})
	if err != nil {
		return nil, fmt.Errorf("list project files: %w", err)
	}
	list, ok := listValue.(string)
	if !ok {
		return nil, fmt.Errorf("unexpected project.list response type %T", listValue)
	}
	out := make(map[string]string)
	for _, item := range parseListLines(list) {
		if item.isDir || !strings.HasSuffix(item.name, ".go") {
			continue
		}
		// The vendored SDK is generated material (a copy of the host plugin
		// SDK, whose own source legitimately contains the ErrNotImplemented
		// constant). It is never agent source; without this exclusion every
		// vendored app fails the stub gate. NoIgnore stays on so .gitignore
		// cannot hide real agent stubs.
		if strings.HasPrefix(item.name, codegen.VendorSDKDir+"/") {
			continue
		}
		path := root + "/" + item.name
		content, err := c.readText(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", item.name, err)
		}
		out[item.name] = content
	}
	return out, nil
}

// listLine is one parsed project.list output line.
type listLine struct {
	name  string
	isDir bool
}

// parseListLines parses project.list output (one root-relative name per line,
// dirs keep a trailing "/"). Marker lines emitted by the project actor
// ("[truncated: …]", "[worktree note] …", "(empty directory)") start with '['
// or '(' and are skipped; detail-mode metadata is stripped when present.
func parseListLines(out string) []listLine {
	var entries []listLine
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "[") || strings.HasPrefix(line, "(") {
			continue
		}
		if m := detailLineRe.FindStringSubmatch(line); m != nil {
			line = m[1]
		}
		e := listLine{name: line}
		if strings.HasSuffix(e.name, "/") {
			e.name = strings.TrimSuffix(e.name, "/")
			e.isDir = true
		}
		entries = append(entries, e)
	}
	return entries
}

// detailLineRe matches a detail-mode line "<name> <date> <time> <offset>
// [Size:N]". The greedy (.*) prefix makes the tail match the LAST candidate,
// so file names that themselves contain date-like text still parse.
var detailLineRe = regexp.MustCompile(`^(.*) \d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} [+-]\d{2}:\d{2}( Size:\d+)?$`)

// hashString returns the SHA-256 hex digest of s.
func hashString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
