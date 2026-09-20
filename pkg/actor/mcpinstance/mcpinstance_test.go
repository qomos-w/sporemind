package mcpinstance

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/qomos-w/gospore/actor"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func validCfg(id string) domain.McpServerConfig {
	return domain.McpServerConfig{
		ID:        id,
		Name:      "test-server",
		Transport: TransportStdio,
		Stdio: &domain.McpStdioTransport{
			Command: "node",
			Args:    []string{"server.js"},
			Env:     map[string]string{"API_KEY": "super-secret", "PATH": "/usr/bin"},
		},
		Enabled: true,
	}
}

func validHTTPCfg(id string) domain.McpServerConfig {
	return domain.McpServerConfig{
		ID:        id,
		Name:      "remote-server",
		Transport: TransportHTTP,
		Http: &domain.McpHttpTransport{
			URL:     "https://mcp.example.com/sse",
			Headers: map[string]string{"Authorization": "Bearer tok"},
		},
		Enabled: true,
	}
}

// freshInstance constructs an Actor via NewActor without OnStart so no real
// connection is attempted in unit tests.
func freshInstance(t *testing.T, id string) *Actor {
	t.Helper()
	a, ok := NewActor(validCfg(id))().(*Actor)
	if !ok {
		t.Fatal("NewActor: expected *Actor")
	}
	return a
}

func TestNewActor_SeedsCfg(t *testing.T) {
	a := freshInstance(t, "srv-x")
	if a.cfg.ID != "srv-x" {
		t.Errorf("expected cfg.Id seeded, got %q", a.cfg.ID)
	}
	if a.cfg.Name != "test-server" {
		t.Errorf("expected cfg.Name seeded, got %q", a.cfg.Name)
	}
	if a.cfg.Stdio.Command != "node" {
		t.Errorf("expected stdio command seeded, got %q", a.cfg.Stdio.Command)
	}
}

func TestHandleStatus_NotConnected(t *testing.T) {
	a := freshInstance(t, "srv-1")
	ctx := testutil.HumanCtx(testutil.GenActorID())
	resp, err := a.handleStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if resp.ID != "srv-1" {
		t.Errorf("expected status.id, got %q", resp.ID)
	}
	if resp.Connected {
		t.Error("fresh instance should not be connected")
	}
	if resp.ToolCount != 0 {
		t.Errorf("expected 0 tools, got %d", resp.ToolCount)
	}
}

// TestHandleStatus_NoSecrets: status only carries state fields — never env or
// header values.
func TestHandleStatus_NoSecrets(t *testing.T) {
	a := freshInstance(t, "srv-1")
	ctx := testutil.HumanCtx(testutil.GenActorID())
	resp, err := a.handleStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != "" {
		t.Errorf("expected empty error on fresh instance, got %q", resp.Error)
	}
	if a.cfg.Stdio.Env["API_KEY"] != "super-secret" {
		t.Errorf("status must not mutate stored env, got %q", a.cfg.Stdio.Env["API_KEY"])
	}
}

func TestHandleCallTool_NotConnected(t *testing.T) {
	a := freshInstance(t, "srv-1")
	ctx := testutil.HumanCtx(testutil.GenActorID())
	if _, err := a.handleCallTool(ctx, domain.McpCallToolReq{
		ID: "srv-1", Tool: "echo", Arguments: map[string]any{},
	}); err == nil {
		t.Error("expected error for call on disconnected instance")
	}
}

func TestHandleDisconnect_NotConnected(t *testing.T) {
	a := freshInstance(t, "srv-1")
	ctx := testutil.HumanCtx(testutil.GenActorID())
	resp, err := a.handleDisconnect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Connected {
		t.Error("expected not connected after disconnect of fresh instance")
	}
}

func TestHandleDisconnect_EmitsStatusEvent(t *testing.T) {
	a := freshInstance(t, "srv-1")
	ctx := testutil.HumanCtx(testutil.GenActorID())
	if _, err := a.handleDisconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if len(ctx.EmittedEvents) != 1 {
		t.Fatalf("expected 1 status event, got %d", len(ctx.EmittedEvents))
	}
	ev := ctx.EmittedEvents[0]
	if ev.Kind != EventKind {
		t.Errorf("expected event kind %q, got %q", EventKind, ev.Kind)
	}
	payload, ok := ev.Payload.(domain.McpServerStatusEvent)
	if !ok {
		t.Fatalf("expected McpServerStatusEvent payload, got %T", ev.Payload)
	}
	if payload.Status.Connected {
		t.Error("expected disconnected status in event")
	}
	if payload.Status.ID != "srv-1" {
		t.Errorf("expected event status id srv-1, got %q", payload.Status.ID)
	}
}

// TestEmitStatus_NotifiesParent verifies emitStatus fires the parent
// notification callback (the mcpmanager agent-refresh chain) with the current
// status payload, in addition to the mcp.server_status event.
func TestEmitStatus_NotifiesParent(t *testing.T) {
	a := freshInstance(t, "srv-1")
	ctx := testutil.HumanCtx(testutil.GenActorID())

	notified := make(chan domain.McpServerStatusEvent, 1)
	a.statusNotify = func(ev domain.McpServerStatusEvent) {
		notified <- ev
	}

	a.emitStatus(ctx)

	select {
	case ev := <-notified:
		if ev.Status.ID != "srv-1" {
			t.Errorf("expected notification for srv-1, got %q", ev.Status.ID)
		}
		if ev.Status.Connected {
			t.Error("expected disconnected status in notification")
		}
	case <-time.After(time.Second):
		t.Fatal("emitStatus did not fire the parent notification")
	}
	if len(ctx.EmittedEvents) != 1 {
		t.Errorf("expected 1 status event, got %d", len(ctx.EmittedEvents))
	}
}

func TestHandleConfigure_Disabled(t *testing.T) {
	a := freshInstance(t, "srv-1")
	ctx := testutil.HumanCtx(testutil.GenActorID())
	cfg := validCfg("srv-1")
	cfg.Enabled = false
	status, err := a.handleConfigure(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if status.Connected {
		t.Error("disabled config must not auto-connect")
	}
	if a.cfg.Enabled {
		t.Error("expected a.cfg.Enabled == false after configure")
	}
}

func TestHandleConfigure_InvalidConfig(t *testing.T) {
	a := freshInstance(t, "srv-1")
	ctx := testutil.HumanCtx(testutil.GenActorID())
	bad := validCfg("srv-1")
	bad.Stdio.Command = ""
	if _, err := a.handleConfigure(ctx, bad); err == nil {
		t.Error("expected error for empty stdio command")
	}
	if a.cfg.Name != "test-server" {
		t.Errorf("invalid configure must not mutate stored cfg, got name %q", a.cfg.Name)
	}
}

func TestValidateConfig_StdioValid(t *testing.T) {
	if err := ValidateConfig(validCfg("srv-1")); err != nil {
		t.Errorf("valid stdio cfg should pass, got %v", err)
	}
}

func TestValidateConfig_HTTPValid(t *testing.T) {
	if err := ValidateConfig(validHTTPCfg("srv-1")); err != nil {
		t.Errorf("valid http cfg should pass, got %v", err)
	}
}

func TestValidateConfig_EmptyName(t *testing.T) {
	cfg := validCfg("srv-1")
	cfg.Name = ""
	if err := ValidateConfig(cfg); err == nil {
		t.Error("expected error for empty name")
	}
}

func TestValidateConfig_UnsupportedTransport(t *testing.T) {
	cfg := validCfg("srv-1")
	cfg.Transport = "sse"
	if err := ValidateConfig(cfg); err == nil {
		t.Error("expected error for unsupported transport")
	}
}

func TestValidateConfig_StdioMissingCommand(t *testing.T) {
	cfg := validCfg("srv-1")
	cfg.Stdio.Command = ""
	if err := ValidateConfig(cfg); err == nil {
		t.Error("expected error for empty stdio command")
	}
}

func TestValidateConfig_HTTPMissingURL(t *testing.T) {
	cfg := validHTTPCfg("srv-1")
	cfg.Http.URL = ""
	if err := ValidateConfig(cfg); err == nil {
		t.Error("expected error for empty http url")
	}
}

func TestValidateConfig_HTTPBadScheme(t *testing.T) {
	cfg := validHTTPCfg("srv-1")
	cfg.Http.URL = "ftp://example.com/mcp"
	if err := ValidateConfig(cfg); err == nil {
		t.Error("expected error for non-http scheme")
	}
}

func TestValidateConfig_HTTPMissingHost(t *testing.T) {
	cfg := validHTTPCfg("srv-1")
	cfg.Http.URL = "https://"
	if err := ValidateConfig(cfg); err == nil {
		t.Error("expected error for host-less url")
	}
}

func TestValidateConfig_HTTPValidProxy(t *testing.T) {
	cfg := validHTTPCfg("srv-1")
	cfg.Http.Proxy = "http://proxy.local:3128"
	if err := ValidateConfig(cfg); err != nil {
		t.Errorf("valid proxy should pass, got %v", err)
	}
}

func TestValidateConfig_HTTPProxyMissingHost(t *testing.T) {
	cfg := validHTTPCfg("srv-1")
	cfg.Http.Proxy = "http://"
	if err := ValidateConfig(cfg); err == nil {
		t.Error("expected error for proxy without host")
	}
}

func TestValidateConfig_HTTPProxyBadURL(t *testing.T) {
	cfg := validHTTPCfg("srv-1")
	cfg.Http.Proxy = "://invalid"
	if err := ValidateConfig(cfg); err == nil {
		t.Error("expected error for malformed proxy url")
	}
}

func TestValidateConfig_StdioNilTransport(t *testing.T) {
	cfg := validCfg("srv-1")
	cfg.Stdio = nil
	if err := ValidateConfig(cfg); err == nil {
		t.Error("expected error for missing stdio transport")
	}
}

func TestMergeEnv(t *testing.T) {
	base := []string{"PATH=/usr/bin", "HOME=/root", "KEEP=yes"}
	over := map[string]string{"API_KEY": "secret", "PATH": "/opt/bin", "CLEAR": ""}
	out := mergeEnv(base, over)
	got := make(map[string]string)
	for _, kv := range out {
		if i := strings.IndexByte(kv, '='); i > 0 {
			got[kv[:i]] = kv[i+1:]
		}
	}
	if got["API_KEY"] != "secret" {
		t.Errorf("expected override added, got %q", got["API_KEY"])
	}
	if got["PATH"] != "/opt/bin" {
		t.Errorf("expected PATH overridden, got %q", got["PATH"])
	}
	if _, ok := got["CLEAR"]; ok {
		t.Error("expected CLEAR removed (empty value deletes)")
	}
	if got["HOME"] != "/root" {
		t.Errorf("expected HOME preserved from base, got %q", got["HOME"])
	}
	if got["KEEP"] != "yes" {
		t.Errorf("expected KEEP preserved, got %q", got["KEEP"])
	}
}

func TestMergeEnv_NoOverrides(t *testing.T) {
	base := []string{"A=1"}
	out := mergeEnv(base, nil)
	if len(out) != 1 || out[0] != "A=1" {
		t.Errorf("expected base untouched, got %v", out)
	}
}

// envToMap parses an env slice ("NAME=value") back into a map for assertions.
func envToMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i > 0 {
			m[kv[:i]] = kv[i+1:]
		}
	}
	return m
}

func TestScrubEnv_StripsCredentials(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"HOME=/root",
		"LANG=en_US.UTF-8",
		"aws_access_key_id=AKIA123",
		"GITHUB_TOKEN=ghp_secret",
		"api_key=lowercase-secret",
		"MYSQL_PASSWORD=pass123",
		"DATABASE_SECRET=db-secret",
		"CREDENTIALS_FILE=/tmp/creds.json",
		"MY_APIKEY=sk-proj-x",
		"SPOREMIND_APIKEY=sk-internal",
		"DSH_TOKEN=abc",
	}
	out := envToMap(scrubEnv(env))
	for name := range out {
		switch name {
		case "aws_access_key_id", "GITHUB_TOKEN", "api_key", "MYSQL_PASSWORD",
			"DATABASE_SECRET", "CREDENTIALS_FILE", "MY_APIKEY",
			"SPOREMIND_APIKEY", "DSH_TOKEN":
			t.Errorf("credential-shaped var %q must be scrubbed", name)
		}
	}
	if out["PATH"] != "/usr/bin" {
		t.Errorf("expected PATH to pass through, got %q", out["PATH"])
	}
	if out["HOME"] != "/root" {
		t.Errorf("expected HOME to pass through, got %q", out["HOME"])
	}
	if out["LANG"] != "en_US.UTF-8" {
		t.Errorf("expected LANG to pass through, got %q", out["LANG"])
	}
}

func TestScrubEnv_KeepsNormalVars(t *testing.T) {
	env := []string{"SHELL=/bin/bash", "NODE_ENV=production", "KEEPALIVE=1", "FOO=bar"}
	out := envToMap(scrubEnv(env))
	if len(out) != 4 {
		t.Fatalf("expected all 4 normal vars to survive, got %v", out)
	}
}

func TestScrubEnv_BuildTransportMergeOverridesParent(t *testing.T) {
	// Mirrors buildTransport's stdio composition:
	// cmd.Env = mergeEnv(scrubEnv(os.Environ()), cfg.Stdio.Env)
	parent := []string{
		"PATH=/usr/bin",
		"HOME=/root",
		"API_KEY=parent-secret",
		"SPOREMIND_TOKEN=parent-internal",
	}
	cfgEnv := map[string]string{
		"API_KEY": "user-configured", // user-configured env takes precedence
		"PATH":    "/opt/bin",
		"FOO":     "bar",
		"EMPTY":   "", // empty value deletes
	}
	out := envToMap(mergeEnv(scrubEnv(parent), cfgEnv))

	// Sensitive parent vars stripped...
	if _, ok := out["SPOREMIND_TOKEN"]; ok {
		t.Error("SPOREMIND_TOKEN must be scrubbed from parent env")
	}
	// ...normal parent vars pass through...
	if out["HOME"] != "/root" {
		t.Errorf("expected HOME to pass through, got %q", out["HOME"])
	}
	// ...and user-configured env overrides parent (even when the name is
	// credential-shaped, because the user configured it explicitly).
	if out["API_KEY"] != "user-configured" {
		t.Errorf("expected user-configured API_KEY to win over parent, got %q", out["API_KEY"])
	}
	if out["PATH"] != "/opt/bin" {
		t.Errorf("expected user-configured PATH to win, got %q", out["PATH"])
	}
	if out["FOO"] != "bar" {
		t.Errorf("expected user-configured FOO added, got %q", out["FOO"])
	}
	if _, ok := out["EMPTY"]; ok {
		t.Error("expected EMPTY removed (empty value deletes)")
	}
}

func TestContentToDomain(t *testing.T) {
	cases := []struct {
		name string
		in   mcp.Content
		want domain.McpToolContent
	}{
		{
			name: "text",
			in:   &mcp.TextContent{Text: "hello"},
			want: domain.McpToolContent{Type: "text", Text: "hello"},
		},
		{
			name: "image",
			in:   &mcp.ImageContent{Data: []byte("raw-image-bytes"), MIMEType: "image/png"},
			want: domain.McpToolContent{
				Type:     "image",
				Data:     base64.StdEncoding.EncodeToString([]byte("raw-image-bytes")),
				MimeType: "image/png",
			},
		},
		{
			name: "image without mime",
			in:   &mcp.ImageContent{Data: []byte("abc")},
			want: domain.McpToolContent{
				Type: "image",
				Data: base64.StdEncoding.EncodeToString([]byte("abc")),
			},
		},
		{
			name: "audio degrades to diagnostic placeholder",
			in:   &mcp.AudioContent{Data: make([]byte, 1234), MIMEType: "audio/wav"},
			want: domain.McpToolContent{Type: "text", Text: "[audio content block: type=audio/wav, 1234 bytes]"},
		},
		{
			name: "audio without mime",
			in:   &mcp.AudioContent{Data: []byte("x")},
			want: domain.McpToolContent{Type: "text", Text: "[audio content block: type=, 1 bytes]"},
		},
		{
			name: "embedded resource degrades to diagnostic placeholder",
			in: &mcp.EmbeddedResource{Resource: &mcp.ResourceContents{
				URI:      "https://example.com/pic.png",
				MIMEType: "image/png",
				Blob:     []byte("blob-data"),
			}},
			want: domain.McpToolContent{Type: "text", Text: "[resource block: uri=https://example.com/pic.png, type=image/png, 9 bytes]"},
		},
		{
			name: "embedded text resource reports char count",
			in: &mcp.EmbeddedResource{Resource: &mcp.ResourceContents{
				URI:  "file:///tmp/note.txt",
				Text: "some text",
			}},
			want: domain.McpToolContent{Type: "text", Text: "[resource block: uri=file:///tmp/note.txt, 9 chars text]"},
		},
		{
			name: "embedded resource without payload",
			in:   &mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "file:///empty"}},
			want: domain.McpToolContent{Type: "text", Text: "[resource block: uri=file:///empty]"},
		},
		{
			name: "unknown block falls back to JSON dump",
			in:   &mcp.ResourceLink{Name: "logo", URI: "https://example.com/logo.png"},
			want: domain.McpToolContent{Type: "other", Text: ""}, // Text asserted separately (JSON)
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := contentToDomain([]mcp.Content{tc.in})
			if len(out) != 1 {
				t.Fatalf("expected 1 content block, got %d", len(out))
			}
			got := out[0]
			if got.Type != tc.want.Type || got.MimeType != tc.want.MimeType || got.Data != tc.want.Data {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
			if tc.want.Text != "" {
				if got.Text != tc.want.Text {
					t.Errorf("Text = %q, want %q", got.Text, tc.want.Text)
				}
			} else if tc.want.Type == "other" {
				if !strings.Contains(got.Text, "resource_link") {
					t.Errorf("expected JSON dump to mention resource_link, got %q", got.Text)
				}
			}
		})
	}
}

func TestContentToDomain_MixedBlocks(t *testing.T) {
	out := contentToDomain([]mcp.Content{
		&mcp.TextContent{Text: "here is the result"},
		&mcp.ImageContent{Data: []byte("img"), MIMEType: "image/jpeg"},
		&mcp.AudioContent{Data: make([]byte, 64), MIMEType: "audio/ogg"},
		&mcp.TextContent{Text: "done"},
	})
	want := []domain.McpToolContent{
		{Type: "text", Text: "here is the result"},
		{Type: "image", Data: base64.StdEncoding.EncodeToString([]byte("img")), MimeType: "image/jpeg"},
		{Type: "text", Text: "[audio content block: type=audio/ogg, 64 bytes]"},
		{Type: "text", Text: "done"},
	}
	if len(out) != len(want) {
		t.Fatalf("expected %d content blocks, got %d: %+v", len(want), len(out), out)
	}
	for i := range want {
		if out[i] != want[i] {
			t.Errorf("block %d = %+v, want %+v", i, out[i], want[i])
		}
	}
}

func TestContentToDomain_NilAndNilable(t *testing.T) {
	out := contentToDomain([]mcp.Content{nil, (*mcp.TextContent)(nil), (*mcp.ImageContent)(nil)})
	if len(out) != 0 {
		t.Fatalf("expected 0 content blocks for nil entries, got %d: %+v", len(out), out)
	}
}

func TestSchemaToMap(t *testing.T) {
	m, ok := schemaToMap(map[string]any{"type": "object"})
	if !ok || m["type"] != "object" {
		t.Errorf("expected map passthrough, got %v ok=%v", m, ok)
	}
}

func TestSameEndpoint(t *testing.T) {
	base := validCfg("srv-1")
	envChanged := validCfg("srv-1")
	envChanged.Stdio.Env = map[string]string{"API_KEY": "rotated"}
	if sameEndpoint(base, envChanged) {
		t.Error("env change must count as endpoint change (stdio captures env at process start)")
	}
	cmdChanged := validCfg("srv-1")
	cmdChanged.Stdio.Command = "python"
	if sameEndpoint(base, cmdChanged) {
		t.Error("command change must count as endpoint change")
	}
	if !sameEndpoint(base, validCfg("srv-1")) {
		t.Error("identical configs must share an endpoint")
	}

	httpBase := validHTTPCfg("srv-1")
	httpEnvChanged := validHTTPCfg("srv-1")
	httpEnvChanged.Http.Headers = map[string]string{"Authorization": "Bearer new"}
	if sameEndpoint(httpBase, httpEnvChanged) {
		t.Error("header change must count as endpoint change (http transport captures headers at build time)")
	}
	urlChanged := validHTTPCfg("srv-1")
	urlChanged.Http.URL = "https://other.example.com/mcp"
	if sameEndpoint(httpBase, urlChanged) {
		t.Error("url change must count as endpoint change")
	}
	if !sameEndpoint(httpBase, validHTTPCfg("srv-1")) {
		t.Error("identical http configs must share an endpoint")
	}

	// Proxy change counts as endpoint change.
	proxyChanged := validHTTPCfg("srv-1")
	proxyChanged.Http.Proxy = "http://proxy.local:8080"
	if sameEndpoint(httpBase, proxyChanged) {
		t.Error("proxy change must count as endpoint change")
	}

	sameProxy := validHTTPCfg("srv-1")
	sameProxy.Http.Proxy = "http://proxy.local:8080"
	if !sameEndpoint(proxyChanged, sameProxy) {
		t.Error("same proxy value must share an endpoint")
	}
	if !sameEndpoint(proxyChanged, proxyChanged) {
		t.Error("identical proxy configs must share an endpoint")
	}
}

// ---------------------------------------------------------------------------
// ExecLoop lane registration + call_tool → status concurrency
// ---------------------------------------------------------------------------

// TestOnStart_ExecLoopRegistration verifies that connect/disconnect/
// call_tool/configure are registered on the ExecLoop lane while
// status/tools are stateless (PureContext) snapshot reads, matching the
// lane routing that keeps status/tools responsive during long MCP round trips.
func TestOnStart_ExecLoopRegistration(t *testing.T) {
	cfg := validCfg("srv-exec")
	cfg.Enabled = false // skip auto-connect so OnStart is purely registration
	a, ok := NewActor(cfg)().(*Actor)
	if !ok {
		t.Fatal("NewActor: expected *Actor")
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.RegOpts = make(map[string][]actor.RegisterOption)

	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	// ExecLoop declared with ModeStateful.
	if ctx.Loops[ExecLoop] != actor.ModeStateful {
		t.Errorf("expected ExecLoop=%q registered as ModeStateful, got %v", ExecLoop, ctx.Loops[ExecLoop])
	}

	onExecLoop := []string{"mcpinstance.connect", "mcpinstance.disconnect", "mcpinstance.call_tool", "mcpinstance.configure"}
	stateless := []string{"mcpinstance.status", "mcpinstance.tools"}

	for _, callID := range onExecLoop {
		opts, ok := ctx.RegOpts[callID]
		if !ok {
			t.Errorf("%s: no registration options recorded", callID)
			continue
		}
		if loop := actor.ResolveLoop(opts...); loop != ExecLoop {
			t.Errorf("%s: expected loop %q, got %q", callID, ExecLoop, loop)
		}
	}
	pure := reflect.TypeOf((*actor.PureContext)(nil)).Elem()
	for _, callID := range stateless {
		fn, ok := ctx.Regs[callID]
		if !ok {
			t.Errorf("%s: not registered", callID)
			continue
		}
		if ft := reflect.TypeOf(fn); ft == nil || ft.NumIn() < 1 || ft.In(0) != pure {
			t.Errorf("%s: must be stateless (PureContext)", callID)
		}
		if loop := actor.ResolveLoop(ctx.RegOpts[callID]...); loop != "" {
			t.Errorf("%s: expected default (pure) loop, got %q", callID, loop)
		}
	}
}

// TestHandleCallTool_DoesNotBlockStatus verifies the call_tool handler does
// not hold any lock across the MCP session.CallTool I/O: while a slow tool
// is executing (blocked on a channel), both handleStatus and handleTools
// return instantly from the cached state.
func TestHandleCallTool_DoesNotBlockStatus(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})

	// MCP server with a slow tool that blocks until released.
	s := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "v0.0.1"}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "slow", Description: "blocks"}, func(_ context.Context, _ *mcp.CallToolRequest, _ echoArgs) (*mcp.CallToolResult, any, error) {
		close(entered)
		<-release
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "slow done"}}}, nil, nil
	})
	st, ct := mcp.NewInMemoryTransports()
	ss, err := s.Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer func() { _ = ss.Close() }()

	a := freshInstance(t, "srv-1")
	ctx := testutil.HumanCtx(testutil.GenActorID())

	session, _, err := a.establishSession(ctx.Lifecycle(), validCfg("srv-1"), ct)
	if err != nil {
		t.Fatalf("establishSession: %v", err)
	}
	defer func() { _ = session.Close() }()

	a.mu.Lock()
	a.session = session
	a.connected = true
	a.watcherDone = make(chan struct{})
	a.mu.Unlock()

	callErr := make(chan error, 1)
	go func() {
		_, err := a.handleCallTool(ctx, domain.McpCallToolReq{
			ID: "srv-1", Tool: "slow", Arguments: map[string]any{"text": "x"},
		})
		callErr <- err
	}()

	// Wait for the tool handler to enter the blocking section.
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("tool handler did not enter the blocking section")
	}

	// handleStatus must return instantly.
	statusStart := time.Now()
	status, err := a.handleStatus(ctx)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}
	if !status.Connected {
		t.Error("expected connected status")
	}
	if d := time.Since(statusStart); d > 100*time.Millisecond {
		t.Errorf("handleStatus took %v while call_tool was in flight", d)
	}

	// handleTools must return instantly.
	toolsStart := time.Now()
	tools, err := a.handleTools(ctx)
	if err != nil {
		t.Fatalf("handleTools: %v", err)
	}
	if tools.ID != "srv-1" {
		t.Errorf("expected tools for srv-1, got %q", tools.ID)
	}
	if d := time.Since(toolsStart); d > 100*time.Millisecond {
		t.Errorf("handleTools took %v while call_tool was in flight", d)
	}

	// Release the tool and verify the call completed.
	close(release)
	select {
	case err := <-callErr:
		if err != nil {
			t.Fatalf("handleCallTool: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("call_tool did not complete after release")
	}
}

// ---------------------------------------------------------------------------
// In-memory integration: real initialize handshake + tools/list + tools/call
// ---------------------------------------------------------------------------

type echoArgs struct {
	Text string `json:"text"`
}

// newInMemoryServer spins up an SDK MCP server with one echo tool and returns
// the server session plus the client-side transport to connect against.
func newInMemoryServer(t *testing.T) (*mcp.ServerSession, mcp.Transport) {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "v0.0.1"}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "echo", Description: "echoes text back"}, func(_ context.Context, _ *mcp.CallToolRequest, in echoArgs) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: in.Text}}}, nil, nil
	})
	st, ct := mcp.NewInMemoryTransports()
	ss, err := s.Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	return ss, ct
}

// TestEstablishSession_HandshakeAndListTools drives a real initialize
// handshake and tools/list discovery over an in-memory transport.
func TestEstablishSession_HandshakeAndListTools(t *testing.T) {
	_, ct := newInMemoryServer(t)
	a := freshInstance(t, "srv-1")
	ctx := testutil.HumanCtx(testutil.GenActorID())

	session, tools, err := a.establishSession(ctx.Lifecycle(), validCfg("srv-1"), ct)
	if err != nil {
		t.Fatalf("establishSession: %v", err)
	}
	defer func() { _ = session.Close() }()

	if len(tools) != 1 {
		t.Fatalf("expected 1 tool discovered, got %d", len(tools))
	}
	if tools[0].Name != "echo" {
		t.Errorf("expected tool echo, got %q", tools[0].Name)
	}
	if tools[0].Description != "echoes text back" {
		t.Errorf("expected description propagated, got %q", tools[0].Description)
	}
}

// TestHandleCallTool_ThroughSession verifies the full call path once a session
// is attached: tools/call executes and the response is converted to the wire
// shape.
func TestHandleCallTool_ThroughSession(t *testing.T) {
	_, ct := newInMemoryServer(t)
	a := freshInstance(t, "srv-1")
	ctx := testutil.HumanCtx(testutil.GenActorID())

	session, _, err := a.establishSession(ctx.Lifecycle(), validCfg("srv-1"), ct)
	if err != nil {
		t.Fatalf("establishSession: %v", err)
	}
	defer func() { _ = session.Close() }()

	a.mu.Lock()
	a.session = session
	a.connected = true
	a.watcherDone = make(chan struct{})
	a.mu.Unlock()

	resp, err := a.handleCallTool(ctx, domain.McpCallToolReq{
		ID:        "srv-1",
		Tool:      "echo",
		Arguments: map[string]any{"text": "round trip"},
	})
	if err != nil {
		t.Fatalf("handleCallTool: %v", err)
	}
	if resp.IsError {
		t.Errorf("expected success, got IsError with %q", resp.Error)
	}
	if len(resp.Content) != 1 || resp.Content[0].Text != "round trip" {
		t.Errorf("expected echoed text, got %+v", resp.Content)
	}
}

// TestHandleTools_ReturnsCachedToolList verifies mcpinstance.tools exposes
// the cached tools/list snapshot with the inputSchema serialized verbatim.
func TestHandleTools_ReturnsCachedToolList(t *testing.T) {
	a := freshInstance(t, "srv-1")
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a.mu.Lock()
	a.tools = []ToolInfo{
		{Name: "echo", Description: "echoes text back", InputSchema: map[string]any{"type": "object"}},
		{Name: "bare", Description: "no schema"},
	}
	a.mu.Unlock()

	out, err := a.handleTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if out.ID != "srv-1" || out.Name != "test-server" {
		t.Errorf("expected srv-1/test-server, got %q/%q", out.ID, out.Name)
	}
	if len(out.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(out.Tools))
	}
	if out.Tools[0].Name != "echo" || out.Tools[0].Description != "echoes text back" {
		t.Errorf("tool metadata mismatch: %+v", out.Tools[0])
	}
	if out.Tools[0].InputSchema != `{"type":"object"}` {
		t.Errorf("inputSchema must serialize verbatim, got %q", out.Tools[0].InputSchema)
	}
	if out.Tools[1].InputSchema != "" {
		t.Errorf("expected empty schema for tool without one, got %q", out.Tools[1].InputSchema)
	}
}

// TestRefreshViewLocked_ProjectionIsReadSafe verifies the topology-facing
// ServerViews component carries identity + connection state but never
// transport payloads (env/header key names or values).
func TestRefreshViewLocked_ProjectionIsReadSafe(t *testing.T) {
	a := freshInstance(t, "srv-1")
	a.mu.Lock()
	a.refreshViewLocked()
	a.mu.Unlock()

	if len(a.ServerViews) != 1 {
		t.Fatalf("expected 1 view, got %d", len(a.ServerViews))
	}
	v := a.ServerViews[0]
	if v.ID != "srv-1" || v.Name != "test-server" || v.Transport != TransportStdio || !v.Enabled {
		t.Errorf("view identity mismatch: %+v", v)
	}
	if v.Status.Connected {
		t.Error("fresh instance view must report disconnected")
	}
	if v.Stdio != nil || v.Http != nil {
		t.Errorf("view must not carry transport payloads (key names/values leak), got %+v", v)
	}
	// Stored secrets must remain untouched.
	if a.cfg.Stdio == nil || a.cfg.Stdio.Env["API_KEY"] != "super-secret" {
		t.Error("refreshViewLocked must not mutate stored env")
	}
}

// TestRefreshViewLocked_ReflectsState verifies the component tracks live
// connection state and error.
func TestRefreshViewLocked_ReflectsState(t *testing.T) {
	a := freshInstance(t, "srv-1")
	a.mu.Lock()
	a.connected = true
	a.lastErr = "session lost"
	a.tools = []ToolInfo{{Name: "echo"}}
	a.refreshViewLocked()
	a.mu.Unlock()

	v := a.ServerViews[0]
	if !v.Status.Connected {
		t.Error("expected connected=true")
	}
	if v.Status.ToolCount != 1 {
		t.Errorf("expected tool count 1, got %d", v.Status.ToolCount)
	}
	if v.Status.Error != "session lost" {
		t.Errorf("expected error propagated, got %q", v.Status.Error)
	}
}

// ---------------------------------------------------------------------------
// refreshTools: atomic swap, rollback on failure, serialized notifications
// ---------------------------------------------------------------------------

// TestRefreshTools_NoSessionNoOp verifies refreshTools is a safe no-op when no
// session is attached: the cache must be left untouched.
func TestRefreshTools_NoSessionNoOp(t *testing.T) {
	a := freshInstance(t, "srv-1")
	a.mu.Lock()
	a.tools = []ToolInfo{{Name: "echo"}}
	a.mu.Unlock()

	a.refreshTools()

	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.tools) != 1 || a.tools[0].Name != "echo" {
		t.Errorf("no-session refresh must not touch the cache, got %+v", a.tools)
	}
}

// TestRefreshTools_SuccessUpdatesCache verifies a successful tools/list
// replaces the previous cache snapshot atomically.
func TestRefreshTools_SuccessUpdatesCache(t *testing.T) {
	_, ct := newInMemoryServer(t)
	a := freshInstance(t, "srv-1")
	ctx := testutil.HumanCtx(testutil.GenActorID())

	session, _, err := a.establishSession(ctx.Lifecycle(), validCfg("srv-1"), ct)
	if err != nil {
		t.Fatalf("establishSession: %v", err)
	}
	defer func() { _ = session.Close() }()

	a.mu.Lock()
	a.session = session
	a.tools = []ToolInfo{{Name: "stale"}}
	a.mu.Unlock()

	a.refreshTools()

	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.tools) != 1 || a.tools[0].Name != "echo" {
		t.Errorf("successful refresh must replace the cache with the fetched list, got %+v", a.tools)
	}
}

// TestRefreshTools_NotifiesParentOnToolSwap verifies that a successful
// tool-list refresh fires the parent notification so the mcpmanager can push
// a tools_refresh to mounted agents. Failed refreshes (rollback) must NOT
// notify since the cache is unchanged.
func TestRefreshTools_NotifiesParentOnToolSwap(t *testing.T) {
	_, ct := newInMemoryServer(t)
	a := freshInstance(t, "srv-1")
	ctx := testutil.HumanCtx(testutil.GenActorID())

	notified := make(chan domain.McpServerStatusEvent, 1)
	a.statusNotify = func(ev domain.McpServerStatusEvent) {
		notified <- ev
	}

	session, _, err := a.establishSession(ctx.Lifecycle(), validCfg("srv-1"), ct)
	if err != nil {
		t.Fatalf("establishSession: %v", err)
	}
	defer func() { _ = session.Close() }()

	a.mu.Lock()
	a.session = session
	a.tools = []ToolInfo{{Name: "stale"}}
	a.mu.Unlock()

	a.refreshTools()

	select {
	case ev := <-notified:
		if ev.Status.ID != "srv-1" {
			t.Errorf("expected notification for srv-1, got %q", ev.Status.ID)
		}
		if !ev.Status.Connected {
			t.Error("expected connected status in notification after successful refresh")
		}
	case <-time.After(time.Second):
		t.Fatal("refreshTools did not fire the parent notification on success")
	}
}

// TestRefreshTools_FailedListToolsPreservesOldList verifies the rollback
// contract: when tools/list fails, the previous cache survives untouched.
func TestRefreshTools_FailedListToolsPreservesOldList(t *testing.T) {
	_, ct := newInMemoryServer(t)
	a := freshInstance(t, "srv-1")
	ctx := testutil.HumanCtx(testutil.GenActorID())

	session, _, err := a.establishSession(ctx.Lifecycle(), validCfg("srv-1"), ct)
	if err != nil {
		t.Fatalf("establishSession: %v", err)
	}

	a.mu.Lock()
	a.session = session
	a.tools = []ToolInfo{{Name: "old-tool", Description: "must survive a failed refresh"}}
	a.mu.Unlock()

	// Kill the connection so the next tools/list fails deterministically.
	if err := session.Close(); err != nil {
		t.Fatalf("session.Close: %v", err)
	}

	a.refreshTools()

	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.tools) != 1 || a.tools[0].Name != "old-tool" {
		t.Errorf("failed refresh must roll back to the previous cache, got %+v", a.tools)
	}
}

// TestRefreshTools_ConcurrentRefreshesDoNotCorruptState hammers refreshTools
// from many goroutines against one live session. The refreshGate serializes
// them; the cache must converge to exactly the server's tool list with no
// duplicates or interleaved partial state (run with -race for the data-race
// check).
func TestRefreshTools_ConcurrentRefreshesDoNotCorruptState(t *testing.T) {
	_, ct := newInMemoryServer(t)
	a := freshInstance(t, "srv-1")
	ctx := testutil.HumanCtx(testutil.GenActorID())

	session, _, err := a.establishSession(ctx.Lifecycle(), validCfg("srv-1"), ct)
	if err != nil {
		t.Fatalf("establishSession: %v", err)
	}
	defer func() { _ = session.Close() }()

	a.mu.Lock()
	a.session = session
	a.mu.Unlock()

	const workers = 16
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.refreshTools()
		}()
	}
	wg.Wait()

	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.tools) != 1 || a.tools[0].Name != "echo" {
		t.Errorf("concurrent refreshes corrupted the cache, got %+v", a.tools)
	}
}

// TestRefreshTools_SerializesOnGate verifies the in-flight guard: a refresh
// that arrives while another refresh holds refreshGate must wait instead of
// running concurrently.
func TestRefreshTools_SerializesOnGate(t *testing.T) {
	a := freshInstance(t, "srv-1")

	a.refreshGate.Lock()
	done := make(chan struct{})
	go func() {
		a.refreshTools()
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("refreshTools must block while another refresh holds the gate")
	case <-time.After(50 * time.Millisecond):
	}

	a.refreshGate.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("refreshTools did not resume after the gate was released")
	}
}

// TestRefreshTools_DisconnectRaceDoesNotResurrectCache runs refreshTools
// concurrently with a disconnect and asserts the final state is always fully
// disconnected. In every interleaving the swap's session-identity re-check
// prevents a refresh that captured the old session from repopulating the
// cache after disconnect cleared it.
func TestRefreshTools_DisconnectRaceDoesNotResurrectCache(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	for i := 0; i < 10; i++ {
		_, ct := newInMemoryServer(t)
		a := freshInstance(t, "srv-1")
		session, _, err := a.establishSession(ctx.Lifecycle(), validCfg("srv-1"), ct)
		if err != nil {
			t.Fatalf("establishSession: %v", err)
		}
		a.mu.Lock()
		a.session = session
		a.connected = true
		a.mu.Unlock()

		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); a.refreshTools() }()
		go func() {
			defer wg.Done()
			a.disconnectLocked(ctx)
		}()
		wg.Wait()

		a.mu.Lock()
		if a.session != nil || a.connected || len(a.tools) != 0 {
			t.Errorf("iteration %d: post-disconnect state must be empty, got session=%v connected=%v tools=%d",
				i, a.session != nil, a.connected, len(a.tools))
		}
		a.mu.Unlock()
	}
}

// TestBuildTransport_HTTPProxyEffective verifies that an HTTP transport built
// from a config with Http.Proxy actually wires the proxy into the underlying
// *http.Transport. This is the regression check for the MCP proxy change chain:
// the proxy value must survive from schema → generated type → buildTransport.
func TestBuildTransport_HTTPProxyEffective(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := freshInstance(t, "srv-1")
	cfg := validHTTPCfg("srv-1")
	cfg.Http.URL = "https://mcp.example.com/mcp"
	cfg.Http.Proxy = "http://proxy.local:3128"

	tr, err := a.buildTransport(ctx, cfg)
	if err != nil {
		t.Fatalf("buildTransport: %v", err)
	}

	sct, ok := tr.(*mcp.StreamableClientTransport)
	if !ok {
		t.Fatalf("expected *mcp.StreamableClientTransport, got %T", tr)
	}

	// Unwrap the header-injecting wrapper to reach the cloned *http.Transport.
	wrapper, ok := sct.HTTPClient.Transport.(*headerInjectRoundTripper)
	if !ok {
		t.Fatalf("expected *headerInjectRoundTripper, got %T", sct.HTTPClient.Transport)
	}
	base, ok := wrapper.base.(*http.Transport)
	if !ok {
		t.Fatalf("expected underlying *http.Transport, got %T", wrapper.base)
	}

	req, err := http.NewRequest("GET", cfg.Http.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	got, err := base.Proxy(req)
	if err != nil {
		t.Fatalf("Proxy(req): %v", err)
	}
	if got == nil {
		t.Fatal("expected proxy to be configured on underlying transport, got nil")
	}
	if got.String() != cfg.Http.Proxy {
		t.Errorf("expected proxy %q, got %q", cfg.Http.Proxy, got.String())
	}
}

// TestBuildTransport_HTTPProxyEmptyOmitsProxy verifies that an empty Http.Proxy
// leaves the transport without a proxy function, matching the direct-connect
// behavior.
func TestBuildTransport_HTTPProxyEmptyOmitsProxy(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := freshInstance(t, "srv-1")
	cfg := validHTTPCfg("srv-1")
	cfg.Http.URL = "https://mcp.example.com/mcp"
	cfg.Http.Proxy = ""

	tr, err := a.buildTransport(ctx, cfg)
	if err != nil {
		t.Fatalf("buildTransport: %v", err)
	}
	sct := tr.(*mcp.StreamableClientTransport)
	wrapper := sct.HTTPClient.Transport.(*headerInjectRoundTripper)
	base := wrapper.base.(*http.Transport)

	req, _ := http.NewRequest("GET", cfg.Http.URL, nil)
	got, err := base.Proxy(req)
	if err != nil {
		t.Fatalf("Proxy(req): %v", err)
	}
	if got != nil {
		t.Errorf("expected no proxy for empty config, got %q", got.String())
	}
}

// TestBuildTransport_HTTPProxyHeadersInjected verifies the header-injecting
// wrapper still injects configured headers after the proxy refactor.
func TestBuildTransport_HTTPProxyHeadersInjected(t *testing.T) {
	var gotHeaders http.Header
	var gotURL string
	base := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		gotHeaders = req.Header.Clone()
		gotURL = req.URL.String()
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	})

	rt := headerInjectingTransport(base, map[string]string{
		"Authorization": "Bearer tok",
		"X-Custom":      "custom-value",
	})

	req, err := http.NewRequest("GET", "https://mcp.example.com/mcp", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	_ = resp.Body.Close()

	if gotURL != "https://mcp.example.com/mcp" {
		t.Errorf("expected URL unchanged, got %q", gotURL)
	}
	if gotHeaders.Get("Authorization") != "Bearer tok" {
		t.Errorf("expected Authorization header injected, got %q", gotHeaders.Get("Authorization"))
	}
	if gotHeaders.Get("X-Custom") != "custom-value" {
		t.Errorf("expected X-Custom header injected, got %q", gotHeaders.Get("X-Custom"))
	}
}

// TestBuildTransport_HTTPProxyURLParseError verifies buildTransport surfaces a
// clear error when the configured proxy URL is malformed.
func TestBuildTransport_HTTPProxyURLParseError(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := freshInstance(t, "srv-1")
	cfg := validHTTPCfg("srv-1")
	cfg.Http.Proxy = "://not-a-url"

	if _, err := a.buildTransport(ctx, cfg); err == nil {
		t.Fatal("expected error for malformed proxy URL")
	}
}

// ---------------------------------------------------------------------------
// Auto-reconnect: exponential backoff, budget exhaustion, explicit-disconnect
// cancellation, and the stability-window budget reset. These drive the watcher
// and reconnectLoop over in-memory transports via buildTransportFn, so no
// subprocess or HTTP server is involved.
// ---------------------------------------------------------------------------

// droppableTransport wraps an in-memory client transport so a test can sever
// the connection on demand. Closing the server session directly blocks on its
// in-flight subscription handlers, so a drop is simulated from the client side.
type droppableTransport struct {
	base mcp.Transport

	mu   sync.Mutex
	conn mcp.Connection
}

func (d *droppableTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	c, err := d.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.conn = c
	d.mu.Unlock()
	return c, nil
}

// drop closes the live connection, which unblocks the client session's Wait().
func (d *droppableTransport) drop() {
	d.mu.Lock()
	c := d.conn
	d.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

// newEchoTransport starts an in-memory MCP server exposing a single tool and
// returns the paired client transport, wrapped so a test can drop it on demand.
func newEchoTransport(t *testing.T, tool string) *droppableTransport {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "v0.0.1"}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: tool, Description: "echoes text back"}, func(_ context.Context, _ *mcp.CallToolRequest, in echoArgs) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: in.Text}}}, nil, nil
	})
	st, ct := mcp.NewInMemoryTransports()
	// The returned server session is intentionally not closed: its background
	// handlers are what answer the client, and the test process owns them.
	if _, err := s.Connect(context.Background(), st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	return &droppableTransport{base: ct}
}

// transportStep is one connect result: a live transport or a dial failure.
type transportStep struct {
	transport mcp.Transport
	err       error
}

// transportScript feeds a fixed sequence of transports/errors to connect: the
// nth call returns the nth step. A call past the end is an unexpected connect
// and fails loudly (guards against a reconnect loop that retries too often).
type transportScript struct {
	mu    sync.Mutex
	items []transportStep
	calls int
}

func (s *transportScript) build(_ actor.Context, _ domain.McpServerConfig) (mcp.Transport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	i := s.calls - 1
	if i >= len(s.items) {
		return nil, errors.New("transportScript: unexpected connect")
	}
	return s.items[i].transport, s.items[i].err
}

func (s *transportScript) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// fastReconnectPolicy builds a deterministic, fast policy: constant delay, a
// large stability window so budget resets never fire mid-test.
func fastReconnectPolicy(maxAttempts int, delay time.Duration) reconnectPolicy {
	return reconnectPolicy{
		initialDelay: delay,
		factor:       1.0,
		maxDelay:     delay,
		maxAttempts:  maxAttempts,
		stability:    time.Hour,
	}
}

// snapshotForTest reads connection state under a.mu.
func (a *Actor) snapshotForTest() (connected bool, tools []ToolInfo, lastErr string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.connected, a.tools, a.lastErr
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestReconnectPolicy_BackoffSchedule pins the exponential backoff math and the
// max-delay cap, plus the zero-value withDefaults fallback.
func TestReconnectPolicy_BackoffSchedule(t *testing.T) {
	p := defaultReconnectPolicy()
	if got := p.nextReconnectDelay(1); got != 500*time.Millisecond {
		t.Errorf("attempt 1 delay = %v, want 500ms", got)
	}
	if got := p.nextReconnectDelay(2); got != 750*time.Millisecond {
		t.Errorf("attempt 2 delay = %v, want 750ms", got)
	}
	if got := p.nextReconnectDelay(3); got != 1125*time.Millisecond {
		t.Errorf("attempt 3 delay = %v, want 1.125s", got)
	}
	if got := p.nextReconnectDelay(100); got != reconnectMaxDelay {
		t.Errorf("attempt 100 delay = %v, want cap %v", got, reconnectMaxDelay)
	}
	if got := (reconnectPolicy{}).withDefaults(); got != p {
		t.Errorf("withDefaults = %+v, want %+v", got, p)
	}
}

// TestReconnectLoop_ReconnectsAfterUnexpectedDrop verifies an unexpected
// session close drives the watcher into the reconnect loop, which re-establishes
// the session (and refreshes the tool cache) from a fresh transport.
func TestReconnectLoop_ReconnectsAfterUnexpectedDrop(t *testing.T) {
	dt1 := newEchoTransport(t, "echo")
	dt2 := newEchoTransport(t, "echo2")

	script := &transportScript{items: []transportStep{{transport: dt1}, {transport: dt2}}}
	a := freshInstance(t, "srv-reconnect")
	a.reconnect = fastReconnectPolicy(3, 2*time.Millisecond)
	a.buildTransportFn = script.build

	ctx := testutil.HumanCtx(testutil.GenActorID())
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	defer a.disconnectLocked(ctx)

	if connected, tools, _ := a.snapshotForTest(); !connected || len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("initial connect = connected:%v tools:%v", connected, tools)
	}

	// Drop the live session: Wait() unblocks, the watcher fires, and the loop
	// should reconnect against the second transport.
	dt1.drop()

	waitFor(t, 3*time.Second, "reconnect to the echo2 server", func() bool {
		connected, tools, _ := a.snapshotForTest()
		return connected && len(tools) == 1 && tools[0].Name == "echo2"
	})
	if got := script.callCount(); got < 2 {
		t.Errorf("expected >= 2 connect attempts, got %d", got)
	}
}

// TestReconnectLoop_GivesUpAfterBudget verifies the loop stops after
// maxAttempts failed retries, clears the stale tool cache, and reports the
// exhausted budget.
func TestReconnectLoop_GivesUpAfterBudget(t *testing.T) {
	dt1 := newEchoTransport(t, "echo")

	script := &transportScript{items: []transportStep{
		{transport: dt1},
		{err: errors.New("dial refused")},
		{err: errors.New("dial refused")},
	}}
	a := freshInstance(t, "srv-giveup")
	a.reconnect = fastReconnectPolicy(2, 2*time.Millisecond)
	a.buildTransportFn = script.build

	ctx := testutil.HumanCtx(testutil.GenActorID())
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	defer a.disconnectLocked(ctx)

	dt1.drop()

	waitFor(t, 3*time.Second, "reconnect budget exhaustion", func() bool {
		connected, tools, lastErr := a.snapshotForTest()
		return !connected && len(tools) == 0 && strings.Contains(lastErr, "reconnect failed after 2 attempts")
	})

	a.mu.Lock()
	attempts := a.reconnectAttempts
	a.mu.Unlock()
	if attempts != 3 {
		t.Errorf("expected reconnectAttempts=3 (maxAttempts+1), got %d", attempts)
	}
	if got := script.callCount(); got != 3 {
		t.Errorf("expected 3 connect calls (1 initial + 2 retries), got %d", got)
	}
}

// TestOnStart_AutoConnectFailureRetries verifies a failed startup auto-connect
// (e.g. the MCP server is not up yet after a host restart) enters the standard
// reconnect loop and recovers once the server becomes reachable.
func TestOnStart_AutoConnectFailureRetries(t *testing.T) {
	script := &transportScript{items: []transportStep{
		{err: errors.New("connectex: connection refused")},
		{err: errors.New("connectex: connection refused")},
		{transport: newEchoTransport(t, "echo")},
	}}
	a := freshInstance(t, "srv-startretry")
	a.reconnect = fastReconnectPolicy(5, 2*time.Millisecond)
	a.buildTransportFn = script.build

	ctx := testutil.HumanCtx(testutil.GenActorID())
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	defer a.disconnectLocked(ctx)

	waitFor(t, 3*time.Second, "startup retry to reach the echo server", func() bool {
		connected, tools, _ := a.snapshotForTest()
		return connected && len(tools) == 1 && tools[0].Name == "echo"
	})
	if got := script.callCount(); got < 3 {
		t.Errorf("expected >= 3 connect attempts, got %d", got)
	}
}

// TestReconnectLoop_ExplicitDisconnectCancels verifies a user disconnect cancels
// an in-flight reconnect loop before it makes an attempt.
func TestReconnectLoop_ExplicitDisconnectCancels(t *testing.T) {
	dt1 := newEchoTransport(t, "echo")

	// Long backoff so the loop is parked on its first sleep when we disconnect.
	script := &transportScript{items: []transportStep{
		{transport: dt1},
		{err: errors.New("should not be reached")},
	}}
	a := freshInstance(t, "srv-cancel")
	a.reconnect = fastReconnectPolicy(5, 10*time.Second)
	a.buildTransportFn = script.build

	ctx := testutil.HumanCtx(testutil.GenActorID())
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	dt1.drop()

	waitFor(t, 3*time.Second, "reconnect loop to start", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.reconnectStop != nil
	})

	a.disconnectLocked(ctx)

	waitFor(t, 3*time.Second, "reconnect loop to stop", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.reconnectStop == nil
	})
	if got := script.callCount(); got != 1 {
		t.Errorf("expected no dial after explicit disconnect, got %d connect calls", got)
	}
	a.mu.Lock()
	disabled := a.reconnectDisabled
	a.mu.Unlock()
	if !disabled {
		t.Error("explicit disconnect must set reconnectDisabled")
	}
}

// TestWatchSession_StabilityWindowResetsReconnectBudget verifies a session that
// outlived the stability window earns a fresh reconnect budget (attempts reset
// to 0) when it finally drops.
func TestWatchSession_StabilityWindowResetsReconnectBudget(t *testing.T) {
	dt := newEchoTransport(t, "echo")

	a := freshInstance(t, "srv-stability")
	ctx := testutil.HumanCtx(testutil.GenActorID())

	session, tools, err := a.establishSession(ctx.Lifecycle(), validCfg("srv-stability"), dt)
	if err != nil {
		t.Fatalf("establishSession: %v", err)
	}
	done := make(chan struct{})
	a.mu.Lock()
	a.session = session
	a.connected = true
	a.tools = tools
	a.watcherDone = done
	a.reconnectDisabled = true // isolate the watcher: no auto-reconnect loop
	a.reconnectAttempts = 5    // a partially-consumed budget to be reset
	a.connectedSince = time.Now().Add(-2 * a.reconnect.stability)
	a.mu.Unlock()

	go a.watchSession(ctx, session, done)

	dt.drop()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not exit after the session closed")
	}

	a.mu.Lock()
	attempts := a.reconnectAttempts
	connected := a.connected
	a.mu.Unlock()
	if attempts != 0 {
		t.Errorf("stability window must reset the reconnect budget, got %d", attempts)
	}
	if connected {
		t.Error("expected disconnected after the watcher reconciled")
	}
}
