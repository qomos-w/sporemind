package desktop

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

// raeFakeRemote is an httptest stand-in for a remote sporemind gateway: it
// serves the login endpoint plus the two import callables, records the
// Authorization header and ?target it saw, and can be scripted to fail each
// route independently so the three error classes are exercised.
type raeFakeRemote struct {
	*httptest.Server

	validAgent string

	listAuth   string
	listTarget string
	listStatus int
	listBody   string

	forkAuth    string
	forkTarget  string
	forkBody    string
	forkStatus  int
	forkErrBody string

	loginStatus int

	agents domain.AgentRefListResp
	fork   domain.AgentSessionForkResp
}

func newRAEFakeRemote(t *testing.T) *raeFakeRemote {
	t.Helper()
	f := &raeFakeRemote{
		validAgent: "agent-1",
		agents: domain.AgentRefListResp{Items: []domain.AgentRef{{
			ActorID:      "agent-1",
			DisplayName:  "Alpha",
			AgentKind:    "coder",
			Status:       "active",
			ProjectName:  "proj",
			LastActivity: "2026-09-30T10:00:00Z",
		}}},
		fork: domain.AgentSessionForkResp{
			Session:         domain.Session{ActiveHead: 2},
			SummarySegments: []domain.SummarySegment{{Text: "seg"}},
			Goal:            &gen.SessionGoal{Condition: "ship it"},
			NextIdx:         7,
			NextSeq:         11,
			NextTurnOrder:   3,
		},
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.Close)
	return f
}

func (f *raeFakeRemote) handle(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/user.auth_login":
		var req map[string]string
		_ = json.NewDecoder(r.Body).Decode(&req)
		if f.loginStatus != 0 {
			w.WriteHeader(f.loginStatus)
			_, _ = w.Write([]byte(`{"error":"bad credentials"}`))
			return
		}
		if req["Username"] != "admin" || req["Password"] != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"bad credentials"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Token": "tok-1"})

	case "/api/workspace.list_agents":
		f.listAuth = r.Header.Get("Authorization")
		f.listTarget = r.URL.Query().Get("target")
		if f.listStatus != 0 {
			w.WriteHeader(f.listStatus)
			_, _ = w.Write([]byte(f.listBody))
			return
		}
		if f.listAuth != "Bearer tok-1" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthenticated"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(f.agents)

	case "/api/local.session_fork":
		f.forkAuth = r.Header.Get("Authorization")
		f.forkTarget = r.URL.Query().Get("target")
		raw, _ := json.Marshal(mustDecodeAny(r))
		f.forkBody = string(raw)
		if f.forkStatus != 0 {
			w.WriteHeader(f.forkStatus)
			_, _ = w.Write([]byte(f.forkErrBody))
			return
		}
		if f.forkAuth != "Bearer tok-1" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthenticated"}`))
			return
		}
		if f.forkTarget != f.validAgent {
			// The real gateway resolves ?target against the actor tree and
			// answers 404 when it is gone.
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"service not found"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(f.fork)

	default:
		http.NotFound(w, r)
	}
}

func mustDecodeAny(r *http.Request) any {
	var v any
	_ = json.NewDecoder(r.Body).Decode(&v)
	return v
}

// raeAppWithConnection returns an App holding one saved connection that points
// at the fake remote, with its password sealed through the real key path so
// remoteSession exercises the production decrypt+login flow.
func raeAppWithConnection(t *testing.T, srv *httptest.Server) *App {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse fake remote URL: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("parse fake remote port: %v", err)
	}

	// Keep the per-installation connection key inside the test's temp dir.
	prev := config.DataDir()
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(func() { config.SetDataDirForTest(prev) })

	key, err := loadConnectionKey()
	if err != nil {
		t.Fatalf("loadConnectionKey: %v", err)
	}
	enc, err := sealPassword(key, "pw")
	if err != nil {
		t.Fatalf("sealPassword: %v", err)
	}
	return &App{conns: &connectionsDoc{Connections: []remoteConnection{{
		ID:          "conn-1",
		Name:        "remote",
		Host:        u.Hostname(),
		Port:        port,
		Username:    "admin",
		PasswordEnc: enc,
	}}}}
}

func TestRemoteAgentListProjectsBriefsAndAuthenticates(t *testing.T) {
	fake := newRAEFakeRemote(t)
	app := raeAppWithConnection(t, fake.Server)

	briefs, err := app.RemoteAgentList("conn-1")
	if err != nil {
		t.Fatalf("RemoteAgentList: %v", err)
	}
	if len(briefs) != 1 {
		t.Fatalf("want 1 brief, got %d: %+v", len(briefs), briefs)
	}
	got := briefs[0]
	want := RemoteAgentBrief{
		ActorID:      "agent-1",
		DisplayName:  "Alpha",
		AgentKind:    "coder",
		Status:       "active",
		ProjectName:  "proj",
		LastActivity: "2026-09-30T10:00:00Z",
	}
	if got != want {
		t.Fatalf("brief mismatch:\n got %+v\nwant %+v", got, want)
	}
	if fake.listAuth != "Bearer tok-1" {
		t.Fatalf("list_agents must carry the bearer token, got %q", fake.listAuth)
	}
	if fake.listTarget != "" {
		t.Fatalf("list_agents must not carry a target, got %q", fake.listTarget)
	}

	// The login token must never surface in the returned payload.
	encoded, _ := json.Marshal(briefs)
	if strings.Contains(string(encoded), "tok-1") {
		t.Fatalf("token leaked into the returned briefs: %s", encoded)
	}
}

func TestRemoteAgentContextExportForksCurrentSession(t *testing.T) {
	fake := newRAEFakeRemote(t)
	app := raeAppWithConnection(t, fake.Server)

	ctx, err := app.RemoteAgentContextExport("conn-1", "agent-1")
	if err != nil {
		t.Fatalf("RemoteAgentContextExport: %v", err)
	}
	if ctx.Session.ActiveHead != 2 {
		t.Fatalf("active head not round-tripped: %+v", ctx.Session)
	}
	if len(ctx.SummarySegments) != 1 || ctx.SummarySegments[0].Text != "seg" {
		t.Fatalf("summary segments not round-tripped: %+v", ctx.SummarySegments)
	}
	if ctx.Goal == nil || ctx.Goal.Condition != "ship it" {
		t.Fatalf("goal must be passed through untouched: %+v", ctx.Goal)
	}
	if ctx.NextIdx != 7 || ctx.NextSeq != 11 || ctx.NextTurnOrder != 3 {
		t.Fatalf("cursor fields not round-tripped: %+v", ctx)
	}
	if fake.forkAuth != "Bearer tok-1" {
		t.Fatalf("session_fork must carry the bearer token, got %q", fake.forkAuth)
	}
	if fake.forkTarget != "agent-1" {
		t.Fatalf("session_fork must route to the named agent, got %q", fake.forkTarget)
	}
	// Empty AtTurnID == full current snapshot (single call, no paging).
	if fake.forkBody != "{}" {
		t.Fatalf("session_fork body must request the full snapshot, got %q", fake.forkBody)
	}
}

func TestRemoteAgentListUnknownConnection(t *testing.T) {
	fake := newRAEFakeRemote(t)
	app := raeAppWithConnection(t, fake.Server)

	if _, err := app.RemoteAgentList("nope"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("want connection-not-found error, got %v", err)
	}
	if _, err := app.RemoteAgentContextExport("conn-1", "  "); err == nil || !strings.Contains(err.Error(), "agent id is required") {
		t.Fatalf("want blank-agent-id error, got %v", err)
	}
}

func TestRemoteAgentErrorClassification(t *testing.T) {
	t.Run("login failure", func(t *testing.T) {
		fake := newRAEFakeRemote(t)
		fake.loginStatus = http.StatusUnauthorized
		app := raeAppWithConnection(t, fake.Server)

		_, err := app.RemoteAgentList("conn-1")
		assertErrContains(t, err, "remote login failed")
	})

	t.Run("remote has no such agent", func(t *testing.T) {
		fake := newRAEFakeRemote(t)
		app := raeAppWithConnection(t, fake.Server)

		_, err := app.RemoteAgentContextExport("conn-1", "ghost")
		assertErrContains(t, err, `remote has no agent "ghost"`)
	})

	t.Run("api missing on old remote (404)", func(t *testing.T) {
		fake := newRAEFakeRemote(t)
		fake.listStatus = http.StatusNotFound
		fake.listBody = `{"error":"service not found"}`
		app := raeAppWithConnection(t, fake.Server)

		_, err := app.RemoteAgentList("conn-1")
		assertErrContains(t, err, "API missing")
	})

	t.Run("api missing on old remote (callable not registered)", func(t *testing.T) {
		fake := newRAEFakeRemote(t)
		fake.forkStatus = http.StatusInternalServerError
		// A resolved actor with no such callable: the gateway's cell error.
		fake.forkErrBody = `{"error":"gospore.cell: call ID \"session_fork\" not registered"}`
		app := raeAppWithConnection(t, fake.Server)

		_, err := app.RemoteAgentContextExport("conn-1", "agent-1")
		assertErrContains(t, err, "API missing")
	})
}

func assertErrContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err.Error(), want)
	}
}
