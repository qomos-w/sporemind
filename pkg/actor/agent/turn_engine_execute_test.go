package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/plan"
	"github.com/qomos-w/gospore/promise"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestUnknownToolResult(t *testing.T) {
	result := unknownToolResult(pendingToolCall{ID: "tool-1", LLMName: "read", UnknownTool: true})
	if !result.isErr {
		t.Fatal("unknown tool result should be an error")
	}
	if result.call.ID != "tool-1" {
		t.Fatalf("tool use ID = %q, want tool-1", result.call.ID)
	}
	if result.out != `unknown tool: "read"` {
		t.Fatalf("result = %q, want unknown tool message", result.out)
	}
}

func TestExtractCommandName(t *testing.T) {
	tests := []struct {
		cmd  string
		args []string
		want string
	}{
		{"go version", nil, "go"},
		{"go", []string{"version"}, "go"},
		{"python -m pytest", nil, "python"},
		{"npm", []string{"run", "build"}, "npm"},
		{"/usr/bin/git status", nil, "git"},
		{"", nil, ""},
		{"   ", nil, ""},
	}
	for _, tt := range tests {
		got := extractCommandName(tt.cmd, tt.args)
		if got != tt.want {
			t.Errorf("extractCommandName(%q, %v) = %q, want %q", tt.cmd, tt.args, got, tt.want)
		}
	}
}

func TestShellCommandString(t *testing.T) {
	tests := []struct {
		cmd  string
		args []string
		want string
	}{
		{"go", nil, "go"},
		{"go", []string{"version"}, "go version"},
		{"go", []string{"test", "./..."}, "go test ./..."},
		{"ls", []string{"-la", "sub/"}, "ls -la sub/"},
		{"cat", []string{"file with spaces"}, `cat "file with spaces"`},
	}
	for _, tt := range tests {
		got := shellCommandString(tt.cmd, tt.args)
		if got != tt.want {
			t.Errorf("shellCommandString(%q, %v) = %q, want %q", tt.cmd, tt.args, got, tt.want)
		}
	}
}

func TestShellArgs(t *testing.T) {
	tests := []struct {
		cmd  string
		args []string
		want []string
	}{
		{"go version", nil, []string{"version"}},
		{"go test ./...", nil, []string{"test", "./..."}},
		{"go", []string{"version"}, []string{"version"}},
		{"go", []string{"test", "./..."}, []string{"test", "./..."}},
	}
	for _, tt := range tests {
		got := shellArgs(tt.cmd, tt.args)
		if len(got) != len(tt.want) {
			t.Errorf("shellArgs(%q, %v) = %v, want %v", tt.cmd, tt.args, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("shellArgs(%q, %v)[%d] = %q, want %q", tt.cmd, tt.args, i, got[i], tt.want[i])
			}
		}
	}
}

func TestHasFindNameFlag(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"find . -name \"*.go\"", true},
		{"find . -iname '*.go'", true},
		{"find .", false},
		{"find src -type f", false},
	}
	for _, tt := range tests {
		got := hasFindNameFlag(tt.cmd)
		if got != tt.want {
			t.Errorf("hasFindNameFlag(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

func TestBuildListPayload(t *testing.T) {
	tests := []struct {
		cmd      string
		wantPath string
		wantAll  bool
	}{
		{"ls", ".", false},
		{"ls -la", ".", true},
		{"ls sub/", "sub/", false},
		{"ls -la sub/", "sub/", true},
		{"ls -a sub/", "sub/", true},
		{"ls --all sub/", "sub/", true},
		{"ls sub/ -a", "sub/", true},
		{"ls sub/ --all", "sub/", true},
		{"ls -l sub/ -a", "sub/", true},
	}
	for _, tt := range tests {
		payload, err := buildListPayload(tt.cmd)
		if err != nil {
			t.Fatalf("buildListPayload(%q) error: %v", tt.cmd, err)
		}
		var req domain.FileSystemListReq
		if err := json.Unmarshal(payload, &req); err != nil {
			t.Fatalf("buildListPayload(%q) invalid JSON: %v", tt.cmd, err)
		}
		if req.Path != tt.wantPath {
			t.Errorf("buildListPayload(%q) Path = %q, want %q", tt.cmd, req.Path, tt.wantPath)
		}
		if req.Depth != 0 {
			t.Errorf("buildListPayload(%q) Depth = %d, want 0", tt.cmd, req.Depth)
		}
		if req.All != tt.wantAll {
			t.Errorf("buildListPayload(%q) All = %v, want %v", tt.cmd, req.All, tt.wantAll)
		}
	}
}

func TestBuildFindPayload(t *testing.T) {
	tests := []struct {
		cmd         string
		wantPath    string
		wantPattern string
	}{
		{"find . -name \"*.go\"", ".", "**/*.go"},
		{"find src -name '*.ts'", "src", "**/*.ts"},
		{"find . -iname \"README*\"", ".", "**/README*"},
	}
	for _, tt := range tests {
		payload, err := buildFindPayload(tt.cmd)
		if err != nil {
			t.Fatalf("buildFindPayload(%q) error: %v", tt.cmd, err)
		}
		var req domain.FileSystemGlobReq
		if err := json.Unmarshal(payload, &req); err != nil {
			t.Fatalf("buildFindPayload(%q) invalid JSON: %v", tt.cmd, err)
		}
		if req.Path != tt.wantPath {
			t.Errorf("buildFindPayload(%q) Path = %q, want %q", tt.cmd, req.Path, tt.wantPath)
		}
		if req.Pattern != tt.wantPattern {
			t.Errorf("buildFindPayload(%q) Pattern = %q, want %q", tt.cmd, req.Pattern, tt.wantPattern)
		}
	}
}

func TestBuildRmPayload(t *testing.T) {
	tests := []struct {
		cmd       string
		wantPath  string
		wantRec   bool
		wantForce bool
	}{
		{"rm file.txt", "file.txt", false, true},
		{"rm -rf build/", "build/", true, true},
		{"rm --recursive dir", "dir", true, true},
	}
	for _, tt := range tests {
		payload, err := buildRmPayload(tt.cmd)
		if err != nil {
			t.Fatalf("buildRmPayload(%q) error: %v", tt.cmd, err)
		}
		var req domain.FileSystemRmReq
		if err := json.Unmarshal(payload, &req); err != nil {
			t.Fatalf("buildRmPayload(%q) invalid JSON: %v", tt.cmd, err)
		}
		if req.Path != tt.wantPath {
			t.Errorf("buildRmPayload(%q) Path = %q, want %q", tt.cmd, req.Path, tt.wantPath)
		}
		if req.Recursive != tt.wantRec {
			t.Errorf("buildRmPayload(%q) Recursive = %v, want %v", tt.cmd, req.Recursive, tt.wantRec)
		}
		if req.Force != tt.wantForce {
			t.Errorf("buildRmPayload(%q) Force = %v, want %v", tt.cmd, req.Force, tt.wantForce)
		}
	}
}

func TestBuildGrepPayload(t *testing.T) {
	payload, err := buildGrepPayload("grep -d 2 -i TODO src/")
	if err != nil {
		t.Fatalf("buildGrepPayload error: %v", err)
	}
	var req domain.FileSystemGrepReq
	if err := json.Unmarshal(payload, &req); err != nil {
		t.Fatalf("buildGrepPayload invalid JSON: %v", err)
	}
	if req.Pattern != "TODO" {
		t.Errorf("buildGrepPayload Pattern = %q, want TODO", req.Pattern)
	}
	if req.Path != "src/" {
		t.Errorf("buildGrepPayload Path = %q, want src/", req.Path)
	}
	if req.Depth != 2 {
		t.Errorf("buildGrepPayload Depth = %d, want 2", req.Depth)
	}
	if !req.IgnoreCase {
		t.Error("buildGrepPayload IgnoreCase = false, want true")
	}
}

func TestWrapShellExecStdout(t *testing.T) {
	out := wrapShellExecStdout("hello\n", "", 0)
	var resp domain.ShellExecResp
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("wrapShellExecStdout output is not valid JSON: %v", err)
	}
	if resp.Stdout != "hello\n" {
		t.Errorf("wrapShellExecStdout Stdout = %q, want hello\\n", resp.Stdout)
	}
	if resp.ExitCode != 0 {
		t.Errorf("wrapShellExecStdout ExitCode = %d, want 0", resp.ExitCode)
	}
}

func TestExternalBinaryAllowlist(t *testing.T) {
	for _, name := range []string{"go", "python", "npm", "git", "make"} {
		if !externalBinaryAllowlist[name] {
			t.Errorf("externalBinaryAllowlist missing %q", name)
		}
	}
	for _, name := range []string{"wget", "ssh", "sudo"} {
		if externalBinaryAllowlist[name] {
			t.Errorf("externalBinaryAllowlist should not contain dangerous command %q", name)
		}
	}
}

func TestVFSBuiltins(t *testing.T) {
	for _, name := range []string{"ls", "cat", "mkdir", "rm", "cp", "mv", "pwd", "echo", "grep", "find", "write"} {
		if !vfsBuiltins[name] {
			t.Errorf("vfsBuiltins missing %q", name)
		}
	}
}

func TestShellFilesystemIntercepts(t *testing.T) {
	for _, name := range []string{"ls", "find", "grep", "rm"} {
		if _, ok := shellFilesystemIntercepts[name]; !ok {
			t.Errorf("shellFilesystemIntercepts missing %q", name)
		}
	}
	// cat stays as a VFS builtin because project.read only handles a single file.
	if _, ok := shellFilesystemIntercepts["cat"]; ok {
		t.Error("shellFilesystemIntercepts should not contain multi-file cat")
	}
}

func TestInjectDir(t *testing.T) {
	root := "/d/dev/sporemind"
	if filepath.Separator == '\\' {
		root = `D:\dev\sporemind`
	}
	absWeb := filepath.Join(root, "web")
	absDeep := filepath.Join(root, "pkg", "shell")

	cases := []struct {
		name    string
		input   string
		wantDir string
		wantOK  bool
	}{
		{
			name:    "empty Dir injected as project root",
			input:   `{"Command":"go","Args":["version"],"Dir":""}`,
			wantDir: root,
			wantOK:  true,
		},
		{
			name:    "missing Dir field injected as project root",
			input:   `{"Command":"go","Args":["version"]}`,
			wantDir: root,
			wantOK:  true,
		},
		{
			name:    "relative Dir joined with project root",
			input:   `{"Command":"ls","Dir":"web"}`,
			wantDir: absWeb,
			wantOK:  true,
		},
		{
			name:    "nested relative Dir joined with project root",
			input:   `{"Command":"ls","Dir":"pkg/shell"}`,
			wantDir: absDeep,
			wantOK:  true,
		},
		{
			name:    "malformed JSON returns input unchanged",
			input:   `{not json`,
			wantDir: "",
			wantOK:  false,
		},
	}
	// Absolute paths are platform-shaped. Verify each platform honors its own shape.
	if filepath.Separator == '\\' {
		cases = append(cases, struct {
			name    string
			input   string
			wantDir string
			wantOK  bool
		}{
			name:    "absolute Dir unchanged (windows drive)",
			input:   `{"Command":"ls","Dir":"C:\\other\\path"}`,
			wantDir: `C:\other\path`,
			wantOK:  true,
		})
	} else {
		cases = append(cases, struct {
			name    string
			input   string
			wantDir string
			wantOK  bool
		}{
			name:    "absolute Dir unchanged (posix)",
			input:   `{"Command":"ls","Dir":"/tmp/elsewhere"}`,
			wantDir: "/tmp/elsewhere",
			wantOK:  true,
		})
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := injectDir(tc.input, root)
			if !tc.wantOK {
				if out != tc.input {
					t.Fatalf("expected passthrough on failure, got %q", out)
				}
				return
			}
			var req domain.ShellBashReq
			if err := json.Unmarshal([]byte(out), &req); err != nil {
				t.Fatalf("injectDir produced invalid JSON: %v\noutput: %s", err, out)
			}
			if req.Dir != tc.wantDir {
				t.Errorf("Dir = %q, want %q (output: %s)", req.Dir, tc.wantDir, out)
			}
		})
	}
}

// TestInjectShellDirs_PrefersWorktreeRoot verifies that when the engine holds
// a bound worktree root, shell Dir injection targets the worktree instead of
// the main project root — bound workers' shell commands must land inside
// their worktree.
func TestInjectShellDirs_PrefersWorktreeRoot(t *testing.T) {
	projectRoot := "/repo/main"
	wtRoot := "/repo/.git/wt/worker-1"

	e := &turnEngine{
		startReq:     domain.TurnStartReq{CompiledContext: domain.CompiledContext{ProjectRoot: projectRoot}},
		worktreeRoot: func() string { return wtRoot },
	}
	batch := toolExecutionBatch{calls: []pendingToolCall{
		{CallableID: "project.shell_exec", Input: `{"Command":"go","Args":["build"]}`},
		{CallableID: "shell.bash", Input: `{"Command":"ls"}`},
		{CallableID: "project.read", Input: `{"Path":"x"}`},
	}}
	e.injectShellDirs(batch)

	for _, c := range []struct {
		idx  int
		want string
	}{
		{0, wtRoot}, {1, wtRoot},
	} {
		var req struct {
			Dir string `json:"Dir"`
		}
		if err := json.Unmarshal([]byte(batch.calls[c.idx].Input), &req); err != nil {
			t.Fatalf("invalid JSON: %v: %s", err, batch.calls[c.idx].Input)
		}
		if req.Dir != c.want {
			t.Errorf("call %d Dir = %q, want %q", c.idx, req.Dir, c.want)
		}
	}
	// Non-shell callables untouched.
	if batch.calls[2].Input != `{"Path":"x"}` {
		t.Errorf("project.read input mutated: %s", batch.calls[2].Input)
	}
}

// TestInjectShellDirs_FallsBackToProjectRoot verifies the unbound path keeps
// injecting the main project root.
func TestInjectShellDirs_FallsBackToProjectRoot(t *testing.T) {
	e := &turnEngine{
		startReq:     domain.TurnStartReq{CompiledContext: domain.CompiledContext{ProjectRoot: "/repo/main"}},
		worktreeRoot: func() string { return "" },
	}
	batch := toolExecutionBatch{calls: []pendingToolCall{
		{CallableID: "shell.exec", Input: `{"Command":"go","Args":["version"]}`},
	}}
	e.injectShellDirs(batch)

	var req struct {
		Dir string `json:"Dir"`
	}
	if err := json.Unmarshal([]byte(batch.calls[0].Input), &req); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, batch.calls[0].Input)
	}
	if req.Dir != "/repo/main" {
		t.Errorf("Dir = %q, want /repo/main", req.Dir)
	}
}

// TestInjectDir_RelativeResolvesAgainstProjectRoot is the regression test for
// the bug where the desktop process runs with cwd=cmd/sporemind-desktop and
// shell.bash's filepath.Abs joins relative Dir against cwd, producing a
// non-existent path like cmd/sporemind-desktop/web.
func TestInjectDir_RelativeResolvesAgainstProjectRoot(t *testing.T) {
	root := "/d/dev/sporemind"
	if filepath.Separator == '\\' {
		root = `D:\dev\sporemind`
	}
	input := `{"Command":"ls","Args":[],"Dir":"web","Timeout":30000}`
	out := injectDir(input, root)

	var req domain.ShellBashReq
	if err := json.Unmarshal([]byte(out), &req); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out)
	}
	want := filepath.Join(root, "web")
	if req.Dir != want {
		t.Errorf("Dir = %q, want %q", req.Dir, want)
	}
	// Other fields must survive the rewrite.
	if req.Timeout != 30000 {
		t.Errorf("Timeout = %d, want 30000 (other fields clobbered)", req.Timeout)
	}
	if req.Command != "ls" {
		t.Errorf("Command = %q, want ls", req.Command)
	}
}

// TestInjectCallerAgentID verifies that the turn engine injects the calling
// agent's actor ID into workspace workflow callables so the workspace can
// resolve agent_status for active-workflow validation. A pre-filled
// CallerAgentId (which could be a forged value from the LLM) is unconditionally
// overwritten with the turn engine's authoritative agent ID.
func TestInjectCallerAgentID(t *testing.T) {
	agentID := "01900000000000000000000000000001"
	e := &turnEngine{agentID: agentID}

	cases := []struct {
		name          string
		callableID    string
		input         string
		wantField     string
		wantAgentID   string
		wantUnchanged bool
	}{
		{
			name:        "spawn_assign injects caller agent id",
			callableID:  "workspace.agent_spawn_assign",
			input:       `{"To":"Worker","AgentKind":"worker"}`,
			wantAgentID: agentID,
		},
		{
			name:        "assign injects caller agent id",
			callableID:  "workspace.agent_assign",
			input:       `{"AgentActorId":"a","BoundTaskCardId":"c"}`,
			wantAgentID: agentID,
		},
		{
			name:        "review injects caller agent id",
			callableID:  "workspace.agent_review",
			input:       `{"AgentActorId":"a","Decision":"approve"}`,
			wantAgentID: agentID,
		},
		{
			name:        "terminate injects caller agent id",
			callableID:  "workspace.agent_terminate",
			input:       `{"AgentActorId":"a"}`,
			wantAgentID: agentID,
		},
		{
			name:        "list_agents injects caller agent id",
			callableID:  "workspace.list_agents",
			input:       `{}`,
			wantAgentID: agentID,
		},
		{
			name:        "send_message injects caller agent id",
			callableID:  "workspace.agent_send_message",
			input:       `{"ToAgentId":"01900000000000000000000000000002","Message":"work"}`,
			wantAgentID: agentID,
		},
		{
			name:        "send_message forged caller agent id overwritten",
			callableID:  "workspace.agent_send_message",
			input:       `{"ToAgentId":"01900000000000000000000000000002","Message":"work","CallerAgentId":"forged-parent-id"}`,
			wantAgentID: agentID,
		},
		{
			name:        "read_message injects caller agent id",
			callableID:  "workspace.agent_read_message",
			input:       `{"Limit":10}`,
			wantAgentID: agentID,
		},
		{
			name:        "read_message forged caller agent id overwritten",
			callableID:  "workspace.agent_read_message",
			input:       `{"Limit":10,"CallerAgentId":"forged-parent-id"}`,
			wantAgentID: agentID,
		},
		{
			name:          "non-workflow callable unchanged",
			callableID:    "project.read",
			input:         `{}`,
			wantUnchanged: true,
		},
		{
			name:        "forged caller agent id overwritten",
			callableID:  "workspace.agent_spawn_assign",
			input:       `{"To":"Worker","AgentKind":"worker","CallerAgentId":"forged-parent-id"}`,
			wantAgentID: agentID,
		},
		{
			name:        "project.review_changeset injects caller agent id",
			callableID:  "project.review_changeset",
			input:       `{"AgentActorID":"019fd32f831b00000000000000000999"}`,
			wantAgentID: agentID,
		},
		{
			name:        "project.review_file_content injects caller agent id",
			callableID:  "project.review_file_content",
			input:       `{"AgentActorID":"019fd32f831b00000000000000000999","FilePath":"main.go"}`,
			wantAgentID: agentID,
		},
		{
			name:        "project.review_changeset forged caller agent id overwritten",
			callableID:  "project.review_changeset",
			input:       `{"AgentActorID":"019fd32f831b00000000000000000999","CallerAgentId":"forged-parent-id"}`,
			wantAgentID: agentID,
		},
		{
			name:        "appmanager.dev_generate injects caller agent id",
			callableID:  "appmanager.dev_generate",
			input:       `{"ProjectId":"019fd32f831b00000000000000000123"}`,
			wantAgentID: agentID,
		},
		{
			name:        "appmanager.dev_gate forged caller agent id overwritten",
			callableID:  "appmanager.dev_gate",
			input:       `{"ProjectId":"019fd32f831b00000000000000000123","CallerAgentId":"forged-agent"}`,
			wantAgentID: agentID,
		},
		{
			name:        "appmanager.register_project injects caller agent id",
			callableID:  "appmanager.register_project",
			input:       `{"ProjectId":"019fd32f831b00000000000000000123"}`,
			wantAgentID: agentID,
		},
		{
			name:        "appmanager.reload_project injects caller agent id for project defaulting",
			callableID:  "appmanager.reload_project",
			input:       `{"AppId":"app.totp"}`,
			wantAgentID: agentID,
		},
		{
			name:        "appmanager.sdk_vendor injects caller agent id",
			callableID:  "appmanager.sdk_vendor",
			input:       `{"AppDir":"probe-app"}`,
			wantAgentID: agentID,
		},
		{
			name:        "appmanager.invoke injects AgentId",
			callableID:  "appmanager.invoke",
			input:       `{"Id":"app.cloud.authenticator","Callable":"codes"}`,
			wantField:   "AgentId",
			wantAgentID: agentID,
		},
		{
			name:        "appmanager.invoke overwrites forged AgentId",
			callableID:  "appmanager.invoke",
			input:       `{"Id":"app.x","Callable":"codes","AgentId":"forged-agent"}`,
			wantField:   "AgentId",
			wantAgentID: agentID,
		},
		{
			name:        "appmanager.cast injects AgentId",
			callableID:  "appmanager.cast",
			input:       `{"Id":"app.x","Event":"changed"}`,
			wantField:   "AgentId",
			wantAgentID: agentID,
		},
		{
			name:        "appmanager.agent_action injects AgentId",
			callableID:  "appmanager.agent_action",
			input:       `{"AppId":"app.x","Action":"create"}`,
			wantField:   "AgentId",
			wantAgentID: agentID,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			batch := toolExecutionBatch{calls: []pendingToolCall{{
				CallableID: tc.callableID,
				Input:      tc.input,
			}}}
			e.injectCallerAgentID(batch)
			if tc.wantUnchanged {
				if batch.calls[0].Input != tc.input {
					t.Errorf("input changed unexpectedly: %s", batch.calls[0].Input)
				}
				return
			}
			var got struct {
				CallerAgentID string `json:"CallerAgentId"`
				AgentID       string `json:"AgentId"`
			}
			if err := json.Unmarshal([]byte(batch.calls[0].Input), &got); err != nil {
				t.Fatalf("invalid JSON after injection: %v", err)
			}
			field := tc.wantField
			if field == "" {
				field = "CallerAgentId"
			}
			gotValue := got.CallerAgentID
			if field == "AgentId" {
				gotValue = got.AgentID
			}
			if gotValue != tc.wantAgentID {
				t.Errorf("%s = %q, want %q", field, gotValue, tc.wantAgentID)
			}
		})
	}
}

// TestInjectCallerAgentID_WorkspaceID pins the WorkspaceId stamping contract
// for appmanager.invoke: the turn engine's workspace actor id is injected
// (and a forged LLM-supplied value overwritten) so plugin-issued llm.*
// reverse calls can be attributed in aistats.
func TestInjectCallerAgentID_WorkspaceID(t *testing.T) {
	agentID := "01900000000000000000000000000001"
	workspaceID := "01900000000000000000000000000077"
	e := &turnEngine{agentID: agentID, workspaceID: workspaceID}

	batch := toolExecutionBatch{calls: []pendingToolCall{{
		CallableID: "appmanager.invoke",
		Input:      `{"Id":"app.x","Callable":"codes","WorkspaceId":"forged-ws"}`,
	}}}
	e.injectCallerAgentID(batch)
	var got struct {
		AgentID     string `json:"AgentId"`
		WorkspaceID string `json:"WorkspaceId"`
	}
	if err := json.Unmarshal([]byte(batch.calls[0].Input), &got); err != nil {
		t.Fatalf("invalid JSON after injection: %v", err)
	}
	if got.WorkspaceID != workspaceID {
		t.Errorf("WorkspaceId = %q, want %q (forged value must be overwritten)", got.WorkspaceID, workspaceID)
	}
	if got.AgentID != agentID {
		t.Errorf("AgentId = %q, want %q", got.AgentID, agentID)
	}

	// Engine without a workspace id must not add the field.
	e2 := &turnEngine{agentID: agentID}
	batch2 := toolExecutionBatch{calls: []pendingToolCall{{
		CallableID: "appmanager.invoke",
		Input:      `{"Id":"app.x","Callable":"codes"}`,
	}}}
	e2.injectCallerAgentID(batch2)
	var got2 struct {
		WorkspaceID *string `json:"WorkspaceId"`
	}
	if err := json.Unmarshal([]byte(batch2.calls[0].Input), &got2); err != nil {
		t.Fatalf("invalid JSON after injection: %v", err)
	}
	if got2.WorkspaceID != nil {
		t.Errorf("WorkspaceId should be absent when engine has none, got %q", *got2.WorkspaceID)
	}
}

// TestInjectCallerAgentID_NoAgentID verifies no injection happens when the
// engine has no agent ID (e.g., test fixtures or early bootstrap).
func TestInjectCallerAgentID_NoAgentID(t *testing.T) {
	e := &turnEngine{agentID: ""}
	batch := toolExecutionBatch{calls: []pendingToolCall{{
		CallableID: "workspace.agent_spawn_assign",
		Input:      `{"To":"Worker","AgentKind":"worker"}`,
	}}}
	e.injectCallerAgentID(batch)
	var got struct {
		CallerAgentID string `json:"CallerAgentId"`
	}
	if err := json.Unmarshal([]byte(batch.calls[0].Input), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got.CallerAgentID != "" {
		t.Errorf("CallerAgentId = %q, want empty when engine has no agent ID", got.CallerAgentID)
	}
}

// TestInjectCallerAgentID_NoPermissionModeInjection verifies that the turn
// engine no longer injects the caller's live permission mode into spawn_assign
// or any other callable. Spawned agents inherit the account-global permission
// mode (resolved by the workspace), not the parent agent's live override.
func TestInjectCallerAgentID_NoPermissionModeInjection(t *testing.T) {
	e := &turnEngine{
		agentID:            "01900000000000000000000000000001",
		livePermissionMode: func() string { return "yolo" },
	}
	// spawn_assign: a pre-existing PermissionMode must be left untouched (the
	// workspace ignores it); no value is injected or overwritten.
	batch := toolExecutionBatch{calls: []pendingToolCall{{
		CallableID: "workspace.agent_spawn_assign",
		Input:      `{"To":"Worker","AgentKind":"worker","PermissionMode":"permission"}`,
	}}}
	e.injectCallerAgentID(batch)
	var got struct {
		CallerAgentID  string `json:"CallerAgentId"`
		PermissionMode string `json:"PermissionMode"`
	}
	if err := json.Unmarshal([]byte(batch.calls[0].Input), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got.CallerAgentID != e.agentID {
		t.Errorf("CallerAgentId = %q, want %q", got.CallerAgentID, e.agentID)
	}
	if got.PermissionMode != "permission" {
		t.Errorf("PermissionMode = %q, want %q (must not be overwritten)", got.PermissionMode, "permission")
	}

	// spawn_by_type: no PermissionMode injected even with a live mode provider.
	batch2 := toolExecutionBatch{calls: []pendingToolCall{{
		CallableID: "workspace.agent_spawn_by_type",
		Input:      `{"AgentKind":"explorer","Description":"d","Prompt":"p"}`,
	}}}
	e.injectCallerAgentID(batch2)
	if strings.Contains(batch2.calls[0].Input, "PermissionMode") {
		t.Errorf("PermissionMode injected into spawn_by_type: %s", batch2.calls[0].Input)
	}

	// Non-spawn callables: no PermissionMode.
	batch3 := toolExecutionBatch{calls: []pendingToolCall{{
		CallableID: "workspace.agent_review",
		Input:      `{"AgentActorId":"a","Decision":"approve"}`,
	}}}
	e.injectCallerAgentID(batch3)
	if strings.Contains(batch3.calls[0].Input, "PermissionMode") {
		t.Errorf("PermissionMode injected into non-spawn callable: %s", batch3.calls[0].Input)
	}

	// Nil livePermissionMode: no injection.
	e2 := &turnEngine{agentID: "01900000000000000000000000000001"}
	batch4 := toolExecutionBatch{calls: []pendingToolCall{{
		CallableID: "workspace.agent_spawn_assign",
		Input:      `{"To":"Worker","AgentKind":"worker"}`,
	}}}
	e2.injectCallerAgentID(batch4)
	if strings.Contains(batch4.calls[0].Input, "PermissionMode") {
		t.Errorf("PermissionMode injected despite nil livePermissionMode: %s", batch4.calls[0].Input)
	}
}

// TestExecuteWaitPlanApproval_OnSubmitFailure_ClosesPlanStep verifies that when
// onPlanSubmit returns an error (plan already pending, JSON parse error, empty
// plan fallback, etc.), executeWaitPlanApproval treats it as a normal tool
// error: closes the plan_call step as failed, writes a tool_result(IsError),
// and returns nil so the turn keeps going. The previous implementation
// propagated the error, stranded the step in "running", and triggered the
// generic "turn ended while step was still open" sweep.
func TestExecuteWaitPlanApproval_OnSubmitFailure_ClosesPlanStep(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	const turnID = "turn-plan-1"
	const toolUseID = "plan-tool-1"

	planCall := &pendingToolCall{
		ID:         toolUseID,
		CallableID: "plan_submit",
		Input:      `{"planText":"do thing"}`,
	}

	planStep := newStepPtr(1, turnID, domain.TurnActionToolCall, "plan_submit")
	planStep.ToolUseID = toolUseID
	planStep.State = "running"

	submitErr := errors.New("plan already submitted: a plan is still pending approval")

	var diagReq domain.OracleReportDiagnosticReq
	var diagCalled bool

	e := &turnEngine{
		logger:        newNopActorLogger(),
		openToolCalls: 1,
		stepByID:      map[string]domain.TurnAction{planStep.ID: planStep},
		stepOrder:     []string{planStep.ID},
		batchSteps:    []domain.TurnAction{planStep},
		onPlanSubmit: func(_ actor.Context, _ string) (string, error) {
			return "", submitErr
		},
		onReportDiagnostic: func(_ actor.Context, req domain.OracleReportDiagnosticReq) {
			diagReq = req
			diagCalled = true
		},
	}

	err := e.executeWaitPlanApproval(ctx, turnID, planCall)
	if err != nil {
		t.Fatalf("executeWaitPlanApproval returned non-nil error: %v (want nil so turn keeps going)", err)
	}

	if e.openToolCalls != 0 {
		t.Errorf("openToolCalls = %d, want 0 after plan submit failure", e.openToolCalls)
	}

	closed := e.stepByID[planStep.ID]
	if closed.State != "failed" {
		t.Errorf("plan step State = %q, want failed", closed.State)
	}
	if closed.Error != submitErr.Error() {
		t.Errorf("plan step Error = %q, want %q", closed.Error, submitErr.Error())
	}
	if closed.Output != submitErr.Error() || closed.Text != submitErr.Error() {
		t.Errorf("plan step Output/Text not populated with error message: Output=%q Text=%q", closed.Output, closed.Text)
	}

	var sawBlockAppended, sawStepError bool
	for _, ev := range e.stepEvents {
		switch ev.Kind {
		case "block.appended":
			if ev.Block == nil {
				t.Errorf("block.appended event for plan step had nil Block")
				continue
			}
			if ev.Block.ToolUseID != toolUseID {
				t.Errorf("block.appended ToolUseID = %q, want %q", ev.Block.ToolUseID, toolUseID)
			}
			if !ev.Block.IsError {
				t.Errorf("block.appended IsError = false, want true")
			}
			if ev.Block.Text != submitErr.Error() {
				t.Errorf("block.appended Text = %q, want %q", ev.Block.Text, submitErr.Error())
			}
			sawBlockAppended = true
		case "step.error":
			if ev.Error != submitErr.Error() {
				t.Errorf("step.error Error = %q, want %q", ev.Error, submitErr.Error())
			}
			sawStepError = true
		case "step.closed":
			t.Errorf("unexpected step.closed event for failed plan step")
		}
	}
	if !sawBlockAppended {
		t.Errorf("expected block.appended event for failed plan step, got events: %v", eventKinds(e.stepEvents))
	}
	if !sawStepError {
		t.Errorf("expected step.error event for failed plan step, got events: %v", eventKinds(e.stepEvents))
	}

	var lastToolMsg *domain.ChatMessage
	for i := range e.history {
		if e.history[i].Role == domain.ChatRoleTool {
			lastToolMsg = &e.history[i]
		}
	}
	if lastToolMsg == nil {
		t.Fatalf("expected tool_result message appended to history, history empty")
	}
	var resultBlock *domain.ContentBlock
	for i := range lastToolMsg.Content {
		if lastToolMsg.Content[i].Type == domain.ContentBlockToolResult {
			resultBlock = &lastToolMsg.Content[i]
		}
	}
	if resultBlock == nil {
		t.Fatalf("expected tool_result block in last tool message")
	}
	if resultBlock.ToolUseID != toolUseID {
		t.Errorf("tool_result ToolUseID = %q, want %q", resultBlock.ToolUseID, toolUseID)
	}
	if !resultBlock.IsError {
		t.Errorf("tool_result IsError = false, want true")
	}
	if resultBlock.Text != submitErr.Error() {
		t.Errorf("tool_result Text = %q, want %q", resultBlock.Text, submitErr.Error())
	}

	if !diagCalled {
		t.Fatalf("onReportDiagnostic was not called after plan submit failure")
	}
	if diagReq.Severity != "error" {
		t.Errorf("diagnostic Severity = %q, want error", diagReq.Severity)
	}
	if diagReq.Source != "plan_submit" {
		t.Errorf("diagnostic Source = %q, want plan_submit", diagReq.Source)
	}
	if diagReq.Message != submitErr.Error() {
		t.Errorf("diagnostic Message = %q, want %q", diagReq.Message, submitErr.Error())
	}
	if diagReq.CallableID != "plan_submit" {
		t.Errorf("diagnostic CallableID = %q, want plan.submit", diagReq.CallableID)
	}
	if diagReq.ToolUseID != toolUseID {
		t.Errorf("diagnostic ToolUseID = %q, want %q", diagReq.ToolUseID, toolUseID)
	}
	if diagReq.Input != planCall.Input {
		t.Errorf("diagnostic Input = %q, want %q", diagReq.Input, planCall.Input)
	}
	if diagReq.RawData != planCall.RawToolCall {
		t.Errorf("diagnostic RawData = %q, want %q", diagReq.RawData, planCall.RawToolCall)
	}

	// Final guarantee: closeRemainingOpenSteps must NOT relabel the plan step
	// as "turn ended while step was still open" — it should skip the step
	// because closePlanStepWithError already moved it to "failed".
	beforeEvents := len(e.stepEvents)
	e.closeRemainingOpenSteps(ctx, turnID)
	if len(e.stepEvents) != beforeEvents {
		t.Errorf("closeRemainingOpenSteps emitted %d unexpected events (plan step should already be closed)",
			len(e.stepEvents)-beforeEvents)
	}
	if e.stepByID[planStep.ID].Error != submitErr.Error() {
		t.Errorf("plan step Error mutated by closeRemainingOpenSteps: got %q, want %q (the real submit error)",
			e.stepByID[planStep.ID].Error, submitErr.Error())
	}
}

// TestExecuteWaitPlanApproval_OnCancel_ExpiresPlan verifies that when the
// turn is cancelled while plan approval is pending, executeWaitPlanApproval
// calls onPlanExpire with the requestID and clears resumeRequestID before
// writing cancelled tool results. Without this, the agent's a.plan.Status
// would stay "pending_approval" forever and block future plan_submit calls.
func TestExecuteWaitPlanApproval_OnCancel_ExpiresPlan(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	const turnID = "turn-plan-cancel"
	const toolUseID = "plan-tool-cancel"
	const requestID = "req-plan-cancel"

	planCall := &pendingToolCall{
		ID:         toolUseID,
		CallableID: "plan_submit",
		Input:      `{"planText":"do thing"}`,
	}
	planStep := newStepPtr(1, turnID, domain.TurnActionToolCall, "plan_submit")
	planStep.ToolUseID = toolUseID
	planStep.State = "running"

	var expireCalled bool
	var expireRequestID string

	done := make(chan struct{})
	close(done) // pre-close so the cancel branch fires immediately at select

	e := &turnEngine{
		logger:        newNopActorLogger(),
		openToolCalls: 1,
		done:          done,
		stepByID:      map[string]domain.TurnAction{planStep.ID: planStep},
		stepOrder:     []string{planStep.ID},
		batchSteps:    []domain.TurnAction{planStep},
		onPlanSubmit: func(_ actor.Context, _ string) (string, error) {
			return requestID, nil
		},
		onPlanSubmitted: func(_ actor.Context, _, _ string) {},
		onPlanExpire: func(_ actor.Context, reqID string) {
			expireCalled = true
			expireRequestID = reqID
		},
	}

	err := e.executeWaitPlanApproval(ctx, turnID, planCall)
	if err == nil {
		t.Fatalf("expected non-nil error from cancelled plan approval, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	if !expireCalled {
		t.Fatalf("onPlanExpire was not called on cancel")
	}
	if expireRequestID != requestID {
		t.Errorf("onPlanExpire requestID = %q, want %q", expireRequestID, requestID)
	}
	if e.resumeRequestID != "" {
		t.Errorf("resumeRequestID = %q after cancel, want empty", e.resumeRequestID)
	}
	if e.openToolCalls != 0 {
		t.Errorf("openToolCalls = %d after cancel, want 0", e.openToolCalls)
	}
}

// TestExecuteBlockAskUser_YoloBlocks verifies that in YOLO mode an ask_user call
// is NOT forwarded to the user: the call is closed as an error telling the agent
// to act autonomously, and executeBlockAskUser returns nil without blocking on
// resumeCh (which would hang the test if the legacy wait path were taken).
func TestExecuteBlockAskUser_YoloBlocks(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	const turnID = "turn-yolo-ask"
	const toolUseID = "ask-tool-1"

	askCall := &pendingToolCall{
		ID:         toolUseID,
		CallableID: "ask_user",
		Input:      `{"questions":[]}`,
	}
	batch := toolExecutionBatch{calls: []pendingToolCall{*askCall}}

	askStep := newStepPtr(1, turnID, domain.TurnActionToolCall, "ask_user")
	askStep.ToolUseID = toolUseID
	askStep.State = "running"

	e := &turnEngine{
		logger:             newNopActorLogger(),
		openToolCalls:      1,
		suppressEventEmit:  true,
		stepByID:           map[string]domain.TurnAction{askStep.ID: askStep},
		stepOrder:          []string{askStep.ID},
		batchSteps:         []domain.TurnAction{askStep},
		livePermissionMode: func() string { return "yolo" },
	}

	if err := e.executeBlockAskUser(ctx, turnID, askCall, batch); err != nil {
		t.Fatalf("executeBlockAskUser returned error: %v (want nil so the turn keeps going)", err)
	}
	if e.openToolCalls != 0 {
		t.Errorf("openToolCalls = %d, want 0 after blocking ask_user", e.openToolCalls)
	}
	closed := e.stepByID[askStep.ID]
	if closed.State != "failed" {
		t.Errorf("ask_user step State = %q, want failed", closed.State)
	}
	if !strings.Contains(closed.Output+closed.Error, "disabled in autonomous mode") {
		t.Errorf("ask_user step Output/Error = %q / %q, want it to mention autonomous mode", closed.Output, closed.Error)
	}
}

// TestExecuteWaitPermission_ResolutionTargetsOriginalRequestID verifies that
// the step.interaction_resolved event emitted after the user answers a
// permission request carries the same RequestID (and StepID) as the original
// step.interaction_requested event. If the engine clears resumeRequestID before
// emitting the resolution, non-approving clients never see the step resolve and
// the UI stays stuck at "Waiting for approval".
func TestExecuteWaitPermission_ResolutionTargetsOriginalRequestID(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	const turnID = "turn-perm-1"
	const toolUseID = "tool-use-1"

	call := pendingToolCall{
		ID:         toolUseID,
		CallableID: "project.write",
		Input:      `{"path":"x.txt"}`,
	}
	batch := toolExecutionBatch{calls: []pendingToolCall{call}}

	toolCallStep := newStepPtr(1, turnID, domain.TurnActionToolCall, "project.write")
	toolCallStep.ToolUseID = toolUseID
	toolCallStep.State = "running"

	resumeCh := make(chan struct{}, 1)
	resumeCh <- struct{}{}

	e := &turnEngine{
		logger:        newNopActorLogger(),
		openToolCalls: 1,
		stepByID:      map[string]domain.TurnAction{toolCallStep.ID: toolCallStep},
		stepOrder:     []string{toolCallStep.ID},
		batchSteps:    []domain.TurnAction{toolCallStep},
		resumeCh:      resumeCh,
		resumeAnswer:  `{"allowed":false}`,
	}

	err := e.executeWaitPermission(ctx, turnID, batch)
	if err != nil {
		t.Fatalf("executeWaitPermission returned error: %v", err)
	}

	var requested, resolved *domain.StepEvent
	for i := range ctx.EmittedEvents {
		if ctx.EmittedEvents[i].Kind != "step" {
			continue
		}
		ev, ok := ctx.EmittedEvents[i].Payload.(domain.StepEvent)
		if !ok {
			continue
		}
		switch ev.Kind {
		case "step.interaction_requested":
			requested = &ev
		case "step.interaction_resolved":
			resolved = &ev
		}
	}

	if requested == nil {
		t.Fatalf("expected step.interaction_requested event, got %d emitted events", len(ctx.EmittedEvents))
	}
	if resolved == nil {
		t.Fatalf("expected step.interaction_resolved event, got %d emitted events", len(ctx.EmittedEvents))
	}

	wantStepID := turnID + "-permission-" + toolUseID
	if requested.StepID != wantStepID {
		t.Errorf("requested StepID = %q, want %q", requested.StepID, wantStepID)
	}
	if resolved.StepID != wantStepID {
		t.Errorf("resolved StepID = %q, want %q (non-approving clients will not see the card close)", resolved.StepID, wantStepID)
	}
	if resolved.RequestID != toolUseID {
		t.Errorf("resolved RequestID = %q, want %q", resolved.RequestID, toolUseID)
	}
	if e.resumeRequestID != "" {
		t.Errorf("resumeRequestID = %q after resolution, want empty", e.resumeRequestID)
	}
}

func TestFilterShortAskUserQuestions(t *testing.T) {
	mkQuestion := func(header, question string) string {
		b, _ := json.Marshal(map[string]any{
			"header":      header,
			"question":    question,
			"options":     []map[string]string{{"label": "a", "description": "b"}},
			"multiSelect": false,
		})
		return string(b)
	}
	mkQuestionRaw := func(header, question string, options any) string {
		b, _ := json.Marshal(map[string]any{
			"header":      header,
			"question":    question,
			"options":     options,
			"multiSelect": false,
		})
		return string(b)
	}

	tests := []struct {
		name            string
		input           string
		wantKeptCount   int
		wantAllFiltered bool
		wantErr         bool
	}{
		{
			name:            "both long enough kept",
			input:           `{"questions":[` + mkQuestion("auth method", "Which auth method should we use?") + `]}`,
			wantKeptCount:   1,
			wantAllFiltered: false,
		},
		{
			name:            "header short, question long → kept (AND)",
			input:           `{"questions":[` + mkQuestion("a", "Which auth method should we use?") + `]}`,
			wantKeptCount:   1,
			wantAllFiltered: false,
		},
		{
			name:            "header long, question short → kept (AND)",
			input:           `{"questions":[` + mkQuestion("auth method", "ok?") + `]}`,
			wantKeptCount:   1,
			wantAllFiltered: false,
		},
		{
			name:            "both too short → filtered",
			input:           `{"questions":[` + mkQuestion("a", "ok") + `]}`,
			wantKeptCount:   0,
			wantAllFiltered: true,
		},
		{
			name:            "boundary: header=3 question=10 kept",
			input:           `{"questions":[` + mkQuestion("abc", "0123456789") + `]}`,
			wantKeptCount:   1,
			wantAllFiltered: false,
		},
		{
			name:            "boundary: header=2 question=9 filtered",
			input:           `{"questions":[` + mkQuestion("ab", "012345678") + `]}`,
			wantKeptCount:   0,
			wantAllFiltered: true,
		},
		{
			name:            "mixed: one filtered, one kept",
			input:           `{"questions":[` + mkQuestion("ab", "012345678") + `,` + mkQuestion("auth method", "Which auth method should we use?") + `]}`,
			wantKeptCount:   1,
			wantAllFiltered: false,
		},
		{
			name:            "empty options array → filtered (OR)",
			input:           `{"questions":[` + mkQuestionRaw("auth method", "Which auth method should we use?", []map[string]string{}) + `]}`,
			wantKeptCount:   0,
			wantAllFiltered: true,
		},
		{
			name:            "missing options field → filtered (OR)",
			input:           `{"questions":[{"header":"auth method","question":"Which auth method should we use?","multiSelect":false}]}`,
			wantKeptCount:   0,
			wantAllFiltered: true,
		},
		{
			name:            "empty options but header/question long → still filtered (OR dominates)",
			input:           `{"questions":[` + mkQuestionRaw("verylongheader", "A very long and complete question text?", []map[string]string{}) + `]}`,
			wantKeptCount:   0,
			wantAllFiltered: true,
		},
		{
			name:            "options empty + header/question short → filtered (both conditions)",
			input:           `{"questions":[` + mkQuestionRaw("ab", "012345678", []map[string]string{}) + `]}`,
			wantKeptCount:   0,
			wantAllFiltered: true,
		},
		{
			name:            "two questions: one options-empty, one valid → one kept",
			input:           `{"questions":[` + mkQuestionRaw("auth method", "Which auth method should we use?", []map[string]string{}) + `,` + mkQuestion("library", "Which UI library should we adopt?") + `]}`,
			wantKeptCount:   1,
			wantAllFiltered: false,
		},
		{
			name:            "empty input → allFiltered",
			input:           ``,
			wantKeptCount:   0,
			wantAllFiltered: true,
		},
		{
			name:            "empty questions array → allFiltered",
			input:           `{"questions":[]}`,
			wantKeptCount:   0,
			wantAllFiltered: true,
		},
		{
			name:    "malformed JSON → error",
			input:   `{not json`,
			wantErr: true,
		},
		{
			name:            "repeated chars in header → filtered",
			input:           `{"questions":[{"header":"aaaaaa","question":"good question?","options":[{"label":"a","description":"b"}],"multiSelect":false}]}`,
			wantKeptCount:   0,
			wantAllFiltered: true,
		},
		{
			name:            "repeated chars in question → filtered",
			input:           `{"questions":[{"header":"ok","question":"What do you think about this aaaaaaa thing?","options":[{"label":"a","description":"b"}],"multiSelect":false}]}`,
			wantKeptCount:   0,
			wantAllFiltered: true,
		},
		{
			name:            "repeated chars in option label → filtered",
			input:           `{"questions":[{"header":"ok","question":"good question?","options":[{"label":"a____a","description":"b"}],"multiSelect":false}]}`,
			wantKeptCount:   0,
			wantAllFiltered: true,
		},
		{
			name:            "repeated chars in option description → filtered",
			input:           `{"questions":[{"header":"ok","question":"good question?","options":[{"label":"a","description":"b------c"}],"multiSelect":false}]}`,
			wantKeptCount:   0,
			wantAllFiltered: true,
		},
		{
			name:            "no repeated run in any field → kept",
			input:           `{"questions":[{"header":"ok","question":"good question?","options":[{"label":"label","description":"some desc"}],"multiSelect":false}]}`,
			wantKeptCount:   1,
			wantAllFiltered: false,
		},
		{
			name:            "mixed: one with repeated run, one clean → one kept",
			input:           `{"questions":[{"header":"aaaa","question":"good question?","options":[{"label":"a","description":"b"}],"multiSelect":false},{"header":"ok","question":"good question?","options":[{"label":"a","description":"b"}],"multiSelect":false}]}`,
			wantKeptCount:   1,
			wantAllFiltered: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, allFiltered, err := filterShortAskUserQuestions(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (out=%q allFiltered=%v)", out, allFiltered)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if allFiltered != tt.wantAllFiltered {
				t.Errorf("allFiltered = %v, want %v", allFiltered, tt.wantAllFiltered)
			}
			if allFiltered {
				return
			}
			var got struct {
				Questions []json.RawMessage `json:"questions"`
			}
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("filtered output is not valid JSON: %v (out=%q)", err, out)
			}
			if len(got.Questions) != tt.wantKeptCount {
				t.Errorf("kept count = %d, want %d (out=%q)", len(got.Questions), tt.wantKeptCount, out)
			}
		})
	}
}

func TestFilterShortAskUserQuestions_PreservesFields(t *testing.T) {
	// 过滤后保留下来的 question 必须原样保留 options/multiSelect 字段。
	input := `{"questions":[` +
		`{"header":"ab","question":"012345678","options":[{"label":"x","description":"y"}],"multiSelect":true},` +
		`{"header":"auth","question":"Which auth method should we use?","options":[{"label":"oauth","description":"OAuth 2.0","recommended":true}],"multiSelect":false}` +
		`]}`
	out, allFiltered, err := filterShortAskUserQuestions(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allFiltered {
		t.Fatalf("allFiltered=true, want false")
	}
	var got struct {
		Questions []struct {
			Header   string `json:"header"`
			Question string `json:"question"`
			Options  []struct {
				Label       string `json:"label"`
				Description string `json:"description"`
				Recommended bool   `json:"recommended"`
			} `json:"options"`
			MultiSelect bool `json:"multiSelect"`
		} `json:"questions"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("filtered output is not valid JSON: %v", err)
	}
	if len(got.Questions) != 1 {
		t.Fatalf("kept count = %d, want 1", len(got.Questions))
	}
	q := got.Questions[0]
	if q.Header != "auth" {
		t.Errorf("header = %q, want %q", q.Header, "auth")
	}
	if len(q.Options) != 1 || q.Options[0].Label != "oauth" || q.Options[0].Description != "OAuth 2.0" {
		t.Errorf("options not preserved: %+v", q.Options)
	}
	if !q.Options[0].Recommended {
		t.Errorf("recommended not preserved: %+v", q.Options[0])
	}
	if q.MultiSelect != false {
		t.Errorf("multiSelect not preserved: %v", q.MultiSelect)
	}
}

func TestHasRepeatedRun(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"", false},
		{"abc", false},
		{"aaa", false},
		{"aaaa", true},
		{"aaaaa", true},
		{"abbbba", true},
		{"----", true},
		{"____", true},
		{"1234", false},
		{"1111", true},
		{"abcde", false},
		{"a__bc", false},
		{"a___a", false},
		{"a____a", true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := hasRepeatedRun(tt.input)
			if got != tt.want {
				t.Errorf("hasRepeatedRun(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalizeAskUserInput(t *testing.T) {
	goodArr := `[{"header":"hdr","question":"what?","options":[{"label":"A"}],"multiSelect":false}]`
	goodJSON := `{"questions":` + goodArr + `}`
	innerEscaped, _ := json.Marshal(goodJSON)
	doubleEncoded := `{"questions":` + string(innerEscaped) + `}`

	tests := []struct {
		name    string
		input   string
		wantErr bool
		wantQs  int // number of questions in normalized output (0 = don't check)
	}{
		{"valid array wrapper", goodJSON, false, 1},
		{"double-encoded string", doubleEncoded, false, 1},
		{"empty input", "", true, 0},
		{"missing questions field", `{"foo":1}`, true, 0},
		{"questions not array", `{"questions":42}`, true, 0},
		{"question missing fields", `{"questions":[{"foo":1}]}`, true, 0},
		{"invalid json", `{not json`, true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := normalizeAskUserInput(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got output: %s", out)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantQs > 0 {
				var raw struct {
					Questions []json.RawMessage `json:"questions"`
				}
				if e := json.Unmarshal([]byte(out), &raw); e != nil {
					t.Fatalf("normalized output not valid JSON: %v", e)
				}
				if len(raw.Questions) != tt.wantQs {
					t.Fatalf("expected %d questions, got %d", tt.wantQs, len(raw.Questions))
				}
			}
		})
	}
}

// concurrentPlanner records how many Call invocations are active at the same
// time so we can assert that fork calls run in parallel.
type concurrentPlanner struct {
	mu        sync.Mutex
	calls     int
	active    int
	maxActive int
}

func (p *concurrentPlanner) Plan(target ref.Ref, callID string, payload any, opts ...plan.Option) (plan.Node, error) {
	return nil, errors.New("not implemented")
}

func (p *concurrentPlanner) Call(ctx context.Context, target ref.Ref, callID string, payload any) *promise.Promise[any] {
	p.mu.Lock()
	p.calls++
	p.active++
	if p.active > p.maxActive {
		p.maxActive = p.active
	}
	p.mu.Unlock()

	// Simulate non-trivial work so overlapping calls are observable.
	time.Sleep(30 * time.Millisecond)

	p.mu.Lock()
	p.active--
	p.mu.Unlock()
	return promise.Resolve[any]("spawned")
}

func (p *concurrentPlanner) Stream(ctx context.Context, target ref.Ref, callID string, payload any, onChunk func(any) error) *promise.Promise[any] {
	return promise.Reject[any](errors.New("not implemented"))
}

func TestExecuteToolBatch_ParallelForkCalls(t *testing.T) {
	const n = 3
	ctx := testutil.HumanCtx(testutil.GenActorID())
	selfRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.SelfRef = selfRef
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "agent" {
			return selfRef, true
		}
		return nil, false
	}

	planner := &concurrentPlanner{}
	ctx.PlannerFn = func() actor.Planner { return planner }

	turnID := "turn-parallel-fork"
	batch := toolExecutionBatch{calls: make([]pendingToolCall, n)}
	batchSteps := make([]domain.TurnAction, n)
	forkKindMap := map[string]string{
		"fork_general": "general",
	}
	for i := 0; i < n; i++ {
		step := newStepPtr(i+1, turnID, domain.TurnActionToolCall, "fork_general")
		step.ToolUseID = fmt.Sprintf("tool-use-%d", i)
		batch.calls[i] = pendingToolCall{
			ID:          step.ToolUseID,
			LLMName:     "fork_general",
			CallableID:  "workspace.agent_spawn_by_type",
			ServiceName: "workspace",
			Input:       `{"Description":"task","Prompt":"do work"}`,
			EffectKind:  domain.EffectNone,
		}
		batchSteps[i] = step
	}

	e := &turnEngine{
		turnID:        turnID,
		forkKindMap:   forkKindMap,
		batchSteps:    batchSteps,
		stepByID:      make(map[string]domain.TurnAction),
		stepOrder:     make([]string, 0, n),
		openToolCalls: n,
		allTools: []domain.ToolSpec{
			{
				Name:        "fork_general",
				CallableID:  "workspace.agent_spawn_by_type",
				ServiceName: "workspace",
				EffectKind:  string(domain.EffectNone),
				InputSchema: `{"type":"object","properties":{"Description":{"type":"string"},"Prompt":{"type":"string"}}}`,
			},
		},
	}

	results := e.executeToolBatch(ctx, batch)
	if len(results) != n {
		t.Fatalf("expected %d results, got %d", n, len(results))
	}
	for i, r := range results {
		if r.isErr {
			t.Fatalf("result %d is error: %s", i, r.out)
		}
	}
	if planner.calls != n {
		t.Fatalf("expected %d planner calls, got %d", n, planner.calls)
	}
	if planner.maxActive < 2 {
		t.Fatalf("expected fork calls to run in parallel (maxActive >= 2), got %d", planner.maxActive)
	}
}

func TestConfirmFileMutatingCallsShellExec(t *testing.T) {
	batch := toolExecutionBatch{
		calls: []pendingToolCall{
			{CallableID: "project.shell_exec", Input: `{"Command":"ls","Dir":"D:/outside/root"}`},
			{CallableID: "project.list", Input: `{"Path":"."}`},
			{CallableID: "project.write", Input: `{"Path":"x","Content":"y"}`},
		},
	}

	confirmFileMutatingCalls(batch)

	// shell_exec must get Confirm=true so yolo-approved dirs outside roots resolve.
	var sh map[string]any
	if err := json.Unmarshal([]byte(batch.calls[0].Input), &sh); err != nil {
		t.Fatalf("shell_exec input unmarshal: %v", err)
	}
	if sh["Confirm"] != true {
		t.Errorf("project.shell_exec: want Confirm=true, got %v", sh["Confirm"])
	}

	// read-only file_list must be untouched.
	var fl map[string]any
	_ = json.Unmarshal([]byte(batch.calls[1].Input), &fl)
	if _, ok := fl["Confirm"]; ok {
		t.Errorf("project.list: Confirm should not be injected, got %v", fl["Confirm"])
	}

	// file_write (already-mutating) still gets Confirm.
	var fw map[string]any
	_ = json.Unmarshal([]byte(batch.calls[2].Input), &fw)
	if fw["Confirm"] != true {
		t.Errorf("project.write: want Confirm=true, got %v", fw["Confirm"])
	}
}

func TestShellStreamBudget(t *testing.T) {
	tests := []struct {
		name    string
		reqMs   int32
		wantMin time.Duration
		wantMax time.Duration
	}{
		{"zero defaults to ToolCallTimeout", 0, domain.ToolCallTimeout, domain.ToolCallTimeout},
		{"negative defaults to ToolCallTimeout", -1, domain.ToolCallTimeout, domain.ToolCallTimeout},
		{"small timeout defaults to ToolCallTimeout", 5000, domain.ToolCallTimeout, domain.ToolCallTimeout},
		{"exceeds default gets margin", 600_000, 11 * time.Minute, 11 * time.Minute},
		{"capped at max", 3_600_000, shellStreamBudgetMax, shellStreamBudgetMax},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shellStreamBudget(tt.reqMs)
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("shellStreamBudget(%d) = %v, want [%v, %v]", tt.reqMs, got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestShellReqTimeoutMs(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int32
	}{
		{"absent", `{"Command":"ls"}`, 0},
		{"explicit", `{"Command":"ls","timeout":120000}`, 120000},
		{"capital key", `{"Command":"ls","Timeout":120000}`, 120000},
		{"invalid json", `{not json`, 0},
		{"empty", ``, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shellReqTimeoutMs(tt.input)
			if got != tt.want {
				t.Errorf("shellReqTimeoutMs(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestShellNoExitCause(t *testing.T) {
	budget := 5 * time.Minute

	t.Run("lifecycle cancelled returns cancelledToolMsg", func(t *testing.T) {
		lc, cancel := context.WithCancel(context.Background())
		cancel()
		msg, cancelled := shellNoExitCause(lc.Err(), nil, budget)
		if !cancelled {
			t.Errorf("want cancelled=true, got false")
		}
		if msg != cancelledToolMsg {
			t.Errorf("msg = %q, want %q", msg, cancelledToolMsg)
		}
	})

	t.Run("callCtx deadline exceeded returns timeout message", func(t *testing.T) {
		callCtx, callCancel := context.WithTimeout(context.Background(), time.Nanosecond)
		defer callCancel()
		time.Sleep(2 * time.Millisecond)
		msg, cancelled := shellNoExitCause(nil, callCtx.Err(), budget)
		if cancelled {
			t.Errorf("want cancelled=false, got true")
		}
		if !strings.Contains(msg, "timed out") {
			t.Errorf("msg = %q, want timeout message", msg)
		}
	})

	t.Run("no error returns fallback", func(t *testing.T) {
		msg, cancelled := shellNoExitCause(nil, nil, budget)
		if cancelled {
			t.Errorf("want cancelled=false, got true")
		}
		if msg != "stream ended without exit chunk" {
			t.Errorf("msg = %q, want fallback", msg)
		}
	})
}

// timeoutStreamPlanner simulates a shell actor that never sends an exit chunk.
// It mirrors the REAL gospore planner.Stream semantics on consumer-ctx
// cancellation (since 9f6ec27): the rejection is translated to the ctx's own
// error before it surfaces — the bare invoke.ErrCallCancelled sentinel is
// reserved for an explicit Cancel without a ctx cause.
type timeoutStreamPlanner struct{}

func (p *timeoutStreamPlanner) Plan(target ref.Ref, callID string, payload any, opts ...plan.Option) (plan.Node, error) {
	return nil, errors.New("not implemented")
}

func (p *timeoutStreamPlanner) Call(ctx context.Context, target ref.Ref, callID string, payload any) *promise.Promise[any] {
	return promise.Reject[any](errors.New("not implemented"))
}

func (p *timeoutStreamPlanner) Stream(ctx context.Context, target ref.Ref, callID string, payload any, onChunk func(any) error) *promise.Promise[any] {
	return promise.Async(func(resolve func(any), reject func(any)) {
		<-ctx.Done()
		reject(ctx.Err())
	})
}

func TestCallStreamingTool_LifecycleCancelled(t *testing.T) {
	e := &turnEngine{
		turnID:            "turn-shell-cancel",
		stepEventMu:       sync.Mutex{},
		suppressEventEmit: true,
	}
	e.pauseCtx, e.pauseCancel = backgroundPauseCtx()

	svcRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		return svcRef, true
	}

	planner := &timeoutStreamPlanner{}
	ctx.PlannerFn = func() actor.Planner { return planner }

	call := pendingToolCall{
		ID:          "tool-shell-2",
		LLMName:     "shell.bash",
		CallableID:  "shell.bash",
		ServiceName: "shell",
		Input:       `{"Command":"sleep 999"}`,
	}

	done := make(chan struct{})
	var out string
	var isErr bool
	go func() {
		out, isErr = e.callStreamingTool(ctx, planner, svcRef, call, "step-2")
		close(done)
	}()

	// Give the stream time to start, then cancel via pause.
	time.Sleep(100 * time.Millisecond)
	e.requestPause()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("callStreamingTool did not return after pause")
	}

	if !isErr {
		t.Fatalf("want isErr=true, got false (out=%q)", out)
	}
	if out != cancelledToolMsg {
		t.Fatalf("out = %q, want %q", out, cancelledToolMsg)
	}
}

// TestCallStreamingTool_PauseRewritesToPausedToolMsg pins the user-pause
// incident shape end-to-end at the tool layer: pause mid-stream → the stream
// rejects with context.Canceled (real planner semantics since gospore
// 9f6ec27) → the result must be cancelledToolMsg so rewritePausedResults
// converts it to pausedToolMsg. Before the context-state classification this
// produced "shell: invoke: call cancelled", the rewrite was a no-op, and the
// LLM saw a raw error instead of "Tool execution paused by user."
func TestCallStreamingTool_PauseRewritesToPausedToolMsg(t *testing.T) {
	e := &turnEngine{
		turnID:            "turn-shell-pause",
		stepEventMu:       sync.Mutex{},
		suppressEventEmit: true,
	}
	e.pauseCtx, e.pauseCancel = backgroundPauseCtx()

	svcRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		return svcRef, true
	}

	planner := &timeoutStreamPlanner{}
	ctx.PlannerFn = func() actor.Planner { return planner }

	call := pendingToolCall{
		ID:          "tool-shell-p",
		LLMName:     "shell.bash",
		CallableID:  "shell.bash",
		ServiceName: "shell",
		Input:       `{"Command":"sleep 999"}`,
	}

	done := make(chan struct{})
	var out string
	var isErr bool
	go func() {
		out, isErr = e.callStreamingTool(ctx, planner, svcRef, call, "step-p")
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)
	e.requestPause()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("callStreamingTool did not return after pause")
	}

	results := []toolExecutionResult{{call: call, out: out, isErr: isErr}}
	e.rewritePausedResults(results)
	if results[0].out != pausedToolMsg {
		t.Fatalf("after rewritePausedResults: out = %q, want %q", results[0].out, pausedToolMsg)
	}
}

// chunkStreamPlanner feeds a fixed chunk sequence through planner.Stream's
// onChunk callback, mimicking a shell actor emitting stdout/stderr chunks and
// a final exit chunk.
type chunkStreamPlanner struct {
	chunks []domain.ShellChunk
}

func (p *chunkStreamPlanner) Plan(target ref.Ref, callID string, payload any, opts ...plan.Option) (plan.Node, error) {
	return nil, errors.New("not implemented")
}

func (p *chunkStreamPlanner) Call(ctx context.Context, target ref.Ref, callID string, payload any) *promise.Promise[any] {
	return promise.Reject[any](errors.New("not implemented"))
}

func (p *chunkStreamPlanner) Stream(ctx context.Context, target ref.Ref, callID string, payload any, onChunk func(any) error) *promise.Promise[any] {
	return promise.Async(func(resolve func(any), reject func(any)) {
		for _, chunk := range p.chunks {
			if err := onChunk(chunk); err != nil {
				resolve(nil) // consumer signalled stop (exit chunk), not an error
				return
			}
		}
		resolve(nil)
	})
}

// shellProgressRecorder collects step.execution_progress events via the
// engine's onStepEvent callback (flushEvents applies buffered events there).
type shellProgressRecorder struct {
	mu     sync.Mutex
	events []domain.StepEvent
}

func (r *shellProgressRecorder) apply(ev domain.StepEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *shellProgressRecorder) progressTexts() []struct {
	stream string
	text   string
} {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []struct {
		stream string
		text   string
	}
	for _, ev := range r.events {
		if ev.Kind != "step.execution_progress" {
			continue
		}
		var payload struct {
			Stream string `json:"stream"`
			Text   string `json:"text"`
		}
		_ = json.Unmarshal([]byte(ev.Progress), &payload)
		out = append(out, struct {
			stream string
			text   string
		}{payload.Stream, payload.Text})
	}
	return out
}

func newShellStreamTestEngine(rec *shellProgressRecorder) *turnEngine {
	e := &turnEngine{
		turnID:            "turn-shell-stream",
		logger:            newNopActorLogger(),
		stepByID:          map[string]domain.TurnAction{},
		suppressEventEmit: true,
	}
	e.onStepEvent = rec.apply
	return e
}

func TestCallStreamingTool_CoalescesRapidChunks(t *testing.T) {
	// 100 chunks × 100B = 10KB of stdout arriving in one burst. Without
	// coalescing this emits 100 step.execution_progress events; with the 4KB
	// size threshold it must collapse to ~ceil(10KB/4KB) flushes.
	const chunkCount = 100
	const chunkSize = 100
	var sb strings.Builder
	chunks := make([]domain.ShellChunk, 0, chunkCount+1)
	for i := 0; i < chunkCount; i++ {
		text := strings.Repeat("x", chunkSize)
		sb.WriteString(text)
		chunks = append(chunks, domain.ShellChunk{Kind: "stdout", Text: text})
	}
	chunks = append(chunks, domain.ShellChunk{
		Kind:     "exit",
		Stdout:   sb.String(),
		ExitCode: 0,
	})

	rec := &shellProgressRecorder{}
	e := newShellStreamTestEngine(rec)
	ctx := testutil.HumanCtx(testutil.GenActorID())
	planner := &chunkStreamPlanner{chunks: chunks}
	call := pendingToolCall{
		ID:         "tool-shell-coalesce",
		LLMName:    "shell.bash",
		CallableID: "shell.bash",
		Input:      `{"Command":"go build"}`,
	}

	out, isErr := e.callStreamingTool(ctx, planner, testutil.NewFakeRef(testutil.GenActorID(), nil), call, "step-coalesce")
	if isErr {
		t.Fatalf("callStreamingTool returned error: %q", out)
	}

	var resp domain.ShellBashResp
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("exit result is not ShellBashResp JSON: %v", err)
	}
	if resp.ExitCode != 0 || resp.Stdout != sb.String() {
		t.Fatalf("exit result = %+v, want ExitCode=0 and full Stdout", resp)
	}

	progresses := rec.progressTexts()
	totalBytes := chunkCount * chunkSize
	// Size threshold bounds: every non-final flush carries ≥4KB, so the count
	// is at least ceil(total/4KB) minus the tail (<4KB) and at most the
	// number of chunks (one per chunk in the worst case).
	minExpected := totalBytes/shellStreamFlushMaxBytes - 1
	if minExpected < 1 {
		minExpected = 1
	}
	if len(progresses) < minExpected {
		t.Fatalf("expected ≥%d progress events, got %d", minExpected, len(progresses))
	}
	if len(progresses) > chunkCount {
		t.Fatalf("expected ≤%d progress events, got %d", chunkCount, len(progresses))
	}

	// Concatenated text must reproduce the full output exactly, in order.
	var got strings.Builder
	for _, p := range progresses {
		if p.stream != "stdout" {
			t.Fatalf("stream = %q, want stdout", p.stream)
		}
		got.WriteString(p.text)
	}
	if got.String() != sb.String() {
		t.Fatalf("concatenated progress text mismatch: got %d bytes, want %d", got.Len(), sb.Len())
	}

	// EventSeq must be strictly increasing across merged flushes.
	rec.mu.Lock()
	var prevSeq int32
	for _, ev := range rec.events {
		if ev.StepID != "step-coalesce" || ev.TurnID != "turn-shell-stream" {
			t.Fatalf("event step/turn = %q/%q, want step-coalesce/turn-shell-stream", ev.StepID, ev.TurnID)
		}
		if ev.EventSeq <= prevSeq {
			t.Fatalf("EventSeq not increasing: prev=%d cur=%d", prevSeq, ev.EventSeq)
		}
		prevSeq = ev.EventSeq
	}
	rec.mu.Unlock()
}

func TestCallStreamingTool_PreservesStreamInterleaving(t *testing.T) {
	// Alternating stdout/stderr chunks must stay in arrival order; adjacent
	// same-stream chunks merge but a stream switch always starts a new event.
	kinds := []string{"stdout", "stderr", "stderr", "stdout", "stdout", "stderr"}
	chunks := make([]domain.ShellChunk, 0, len(kinds)+1)
	var wantStdout, wantStderr strings.Builder
	for i, kind := range kinds {
		text := fmt.Sprintf("line-%d-%s\n", i, kind)
		chunks = append(chunks, domain.ShellChunk{Kind: kind, Text: text})
		if kind == "stdout" {
			wantStdout.WriteString(text)
		} else {
			wantStderr.WriteString(text)
		}
	}
	chunks = append(chunks, domain.ShellChunk{
		Kind:     "exit",
		Stdout:   wantStdout.String(),
		Stderr:   wantStderr.String(),
		ExitCode: 3,
	})

	rec := &shellProgressRecorder{}
	e := newShellStreamTestEngine(rec)
	ctx := testutil.HumanCtx(testutil.GenActorID())
	planner := &chunkStreamPlanner{chunks: chunks}
	call := pendingToolCall{
		ID:         "tool-shell-interleave",
		LLMName:    "shell.bash",
		CallableID: "shell.bash",
		Input:      `{"Command":"make"}`,
	}

	out, isErr := e.callStreamingTool(ctx, planner, testutil.NewFakeRef(testutil.GenActorID(), nil), call, "step-interleave")
	if isErr {
		t.Fatalf("callStreamingTool returned error: %q", out)
	}
	var resp domain.ShellBashResp
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("exit result is not ShellBashResp JSON: %v", err)
	}
	// Non-zero exit code stays a normal result (isErr=false), same as before.
	if resp.ExitCode != 3 || resp.Stdout != wantStdout.String() || resp.Stderr != wantStderr.String() {
		t.Fatalf("exit result = %+v, want ExitCode=3 with full Stdout/Stderr", resp)
	}

	progresses := rec.progressTexts()
	var gotStdout, gotStderr strings.Builder
	prevStream := ""
	for i, p := range progresses {
		if p.stream == prevStream {
			t.Fatalf("event %d: adjacent same-stream %q events were not coalesced", i, p.stream)
		}
		prevStream = p.stream
		if p.stream == "stdout" {
			gotStdout.WriteString(p.text)
		} else if p.stream == "stderr" {
			gotStderr.WriteString(p.text)
		} else {
			t.Fatalf("event %d: unknown stream %q", i, p.stream)
		}
	}
	if gotStdout.String() != wantStdout.String() {
		t.Fatalf("stdout mismatch: got %q, want %q", gotStdout.String(), wantStdout.String())
	}
	if gotStderr.String() != wantStderr.String() {
		t.Fatalf("stderr mismatch: got %q, want %q", gotStderr.String(), wantStderr.String())
	}
}

func TestCallStreamingTool_FlushesTailBelowThreshold(t *testing.T) {
	// Total output stays under the 4KB size threshold: the tail must still be
	// delivered via the forced flush on the exit chunk, unaffected.
	chunks := []domain.ShellChunk{
		{Kind: "stdout", Text: "hello "},
		{Kind: "stdout", Text: "world"},
		{Kind: "exit", Stdout: "hello world", ExitCode: 0},
	}

	rec := &shellProgressRecorder{}
	e := newShellStreamTestEngine(rec)
	ctx := testutil.HumanCtx(testutil.GenActorID())
	planner := &chunkStreamPlanner{chunks: chunks}
	call := pendingToolCall{
		ID:         "tool-shell-tail",
		LLMName:    "shell.bash",
		CallableID: "shell.bash",
		Input:      `{"Command":"echo"}`,
	}

	out, isErr := e.callStreamingTool(ctx, planner, testutil.NewFakeRef(testutil.GenActorID(), nil), call, "step-tail")
	if isErr {
		t.Fatalf("callStreamingTool returned error: %q", out)
	}
	var resp domain.ShellBashResp
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("exit result is not ShellBashResp JSON: %v", err)
	}
	if resp.Stdout != "hello world" {
		t.Fatalf("exit Stdout = %q, want %q", resp.Stdout, "hello world")
	}

	progresses := rec.progressTexts()
	if len(progresses) != 1 {
		t.Fatalf("expected 1 merged progress event, got %d", len(progresses))
	}
	if progresses[0].stream != "stdout" || progresses[0].text != "hello world" {
		t.Fatalf("progress = %+v, want single stdout %q", progresses[0], "hello world")
	}
}

func TestShellStreamFlusher_FlushIfDue(t *testing.T) {
	// Direct unit test of the timer path: buffered text below the size
	// threshold stays put until the interval elapses, then flushIfDue pushes it.
	rec := &shellProgressRecorder{}
	e := newShellStreamTestEngine(rec)
	ctx := testutil.HumanCtx(testutil.GenActorID())
	f := newShellStreamFlusher(ctx, e, "step-timer", "turn-timer")

	f.add("stdout", "pending")
	if got := len(rec.progressTexts()); got != 0 {
		t.Fatalf("flush happened before interval elapsed: %d events", got)
	}

	// Simulate the ticker firing after the interval has passed.
	f.mu.Lock()
	f.lastFlush = time.Now().Add(-2 * shellStreamFlushInterval)
	f.mu.Unlock()
	f.flushIfDue()

	progresses := rec.progressTexts()
	if len(progresses) != 1 || progresses[0].text != "pending" {
		t.Fatalf("after flushIfDue: %d events, want 1 with text %q", len(progresses), "pending")
	}

	// Empty buffer: flushIfDue is a no-op (no spurious flushEvents calls).
	f.flushIfDue()
	if got := len(rec.progressTexts()); got != 1 {
		t.Fatalf("flushIfDue on empty buffer emitted %d extra events", got-1)
	}
}

func TestReportBypassDecision(t *testing.T) {
	const turnID = "turn-bypass-1"
	batch := toolExecutionBatch{calls: []pendingToolCall{
		{ID: "tool-1", LLMName: "write_file", CallableID: "project.write", Input: `{"path":"a.txt"}`},
		{ID: "tool-2", LLMName: "shell", CallableID: "project.shell_exec", Input: `{"command":"go"}`},
	}}

	cases := []struct {
		name        string
		allowed     bool
		reason      string
		wantSev     string
		wantMessage string
	}{
		{"approve", true, "bypass approved by fast model", "info", "bypass approved by fast model"},
		{"deny", false, "bypass denied by fast model", "warning", "bypass denied by fast model"},
		{"deny_on_failure", false, "bypass: intent call failed", "warning", "bypass denied by fast model: bypass: intent call failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var diagReq domain.OracleReportDiagnosticReq
			var diagCalled bool
			e := &turnEngine{
				agentID: "agent-bypass-1",
				logger:  newNopActorLogger(),
				resolveTargets: func(actor.PureContext, domain.ModelSlot) []dispatchTarget {
					return []dispatchTarget{{unit: domain.ModelUnit{Model: "fast-model", Provider: "fast-provider"}}}
				},
				onReportDiagnostic: func(_ actor.Context, req domain.OracleReportDiagnosticReq) {
					diagReq = req
					diagCalled = true
				},
			}
			ctx := testutil.HumanCtx(testutil.GenActorID())
			e.reportBypassDecision(ctx, turnID, batch, tc.allowed, tc.reason)

			if !diagCalled {
				t.Fatal("onReportDiagnostic was not called")
			}
			if diagReq.Severity != tc.wantSev {
				t.Errorf("Severity = %q, want %q", diagReq.Severity, tc.wantSev)
			}
			if diagReq.Source != "permission_bypass" {
				t.Errorf("Source = %q, want permission_bypass", diagReq.Source)
			}
			if diagReq.Message != tc.wantMessage {
				t.Errorf("Message = %q, want %q", diagReq.Message, tc.wantMessage)
			}
			if diagReq.TurnID != turnID {
				t.Errorf("TurnID = %q, want %q", diagReq.TurnID, turnID)
			}
			if diagReq.AgentID != "agent-bypass-1" {
				t.Errorf("AgentID = %q, want agent-bypass-1", diagReq.AgentID)
			}
			if diagReq.CallableID != "project.write,project.shell_exec" {
				t.Errorf("CallableID = %q, want comma-joined batch callable IDs", diagReq.CallableID)
			}
			if diagReq.Unit == nil || diagReq.Unit.Model != "fast-model" || diagReq.Unit.Provider != "fast-provider" {
				t.Errorf("Unit = %+v, want the fast slot unit", diagReq.Unit)
			}
			if !strings.Contains(diagReq.Input, "1. Tool: write_file (project.write)") ||
				!strings.Contains(diagReq.Input, "2. Tool: shell (project.shell_exec)") {
				t.Errorf("Input = %q, want per-call Tool/CallableID listing", diagReq.Input)
			}
		})
	}
}

func TestReportBypassDecision_NilHookAndNoTargets(t *testing.T) {
	e := &turnEngine{
		agentID: "agent-bypass-2",
		logger:  newNopActorLogger(),
		// No resolveTargets, no onReportDiagnostic: must be a no-op, not a panic.
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	e.reportBypassDecision(ctx, "turn-x", toolExecutionBatch{calls: []pendingToolCall{{CallableID: "project.write"}}}, false, "bypass denied by fast model")

	var diagReq domain.OracleReportDiagnosticReq
	var diagCalled bool
	e2 := &turnEngine{
		agentID: "agent-bypass-2",
		logger:  newNopActorLogger(),
		onReportDiagnostic: func(_ actor.Context, req domain.OracleReportDiagnosticReq) {
			diagReq = req
			diagCalled = true
		},
	}
	e2.reportBypassDecision(ctx, "turn-x", toolExecutionBatch{calls: []pendingToolCall{{CallableID: "project.write"}}}, true, "bypass approved by fast model")
	if !diagCalled {
		t.Fatal("onReportDiagnostic was not called")
	}
	if diagReq.Unit != nil {
		t.Errorf("Unit = %+v, want nil when no fast targets resolve", diagReq.Unit)
	}
}

func TestBypassBatchSummary_TruncatesLongInput(t *testing.T) {
	batch := toolExecutionBatch{calls: []pendingToolCall{
		{LLMName: "write_file", CallableID: "project.write", Input: strings.Repeat("x", 3000)},
	}}
	summary := bypassBatchSummary(batch)
	if !strings.Contains(summary, "…(truncated)") {
		t.Fatalf("summary missing truncation marker: %q", truncate(summary, 100))
	}
	if len(summary) > 2000+200 {
		t.Errorf("summary length = %d, want per-call input capped at 2000 chars", len(summary))
	}
}

func TestBypassContextSummary_WorktreeVsMainRepo(t *testing.T) {
	// 有 worktree：上下文必须标明 DISPOSABLE worktree 路径与 roots，
	// fast model 才能把 worktree 内删除判为安全。
	e := &turnEngine{
		projectID:    "proj-1",
		roots:        []string{"/workspace"},
		worktreeRoot: func() string { return "/workspace/.worktrees/wt-1" },
	}
	s := e.bypassContextSummary()
	for _, want := range []string{"proj-1", "/workspace", "DISPOSABLE git worktree", "/workspace/.worktrees/wt-1"} {
		if !strings.Contains(s, want) {
			t.Errorf("worktree context missing %q in:\n%s", want, s)
		}
	}

	// 无 worktree：标明 MAIN repository，删除不可恢复。
	e2 := &turnEngine{projectID: "proj-1"}
	s2 := e2.bypassContextSummary()
	if !strings.Contains(s2, "MAIN repository") || !strings.Contains(s2, "NOT recoverable") {
		t.Errorf("main-repo context must warn deletions are not recoverable:\n%s", s2)
	}
}

func TestToolCallTimeoutFor(t *testing.T) {
	// Plugin callables routed through appmanager.invoke get the full native
	// tool budget; media generation keeps its own budget; everything else falls
	// back to the generic tool-call timeout.
	tests := []struct {
		callableID string
		want       time.Duration
	}{
		{"appmanager.invoke", nativeToolTimeout},
		{"image_generate", mediaGenerationTimeout},
		{"video_generate", mediaGenerationTimeout},
		{"project.write", domain.ToolCallTimeout},
		{"shell.exec", domain.ToolCallTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.callableID, func(t *testing.T) {
			if got := toolCallTimeoutFor(tt.callableID); got != tt.want {
				t.Errorf("toolCallTimeoutFor(%q) = %v, want %v", tt.callableID, got, tt.want)
			}
		})
	}
}
