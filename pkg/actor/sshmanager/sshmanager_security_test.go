package sshmanager

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestAuthorizeSSHIdentityRejectsAnonymous(t *testing.T) {
	if _, err := authorizeSSHIdentity(id.Identity{}); err == nil {
		t.Fatal("anonymous identity must be denied")
	}
	if _, err := authorizeSSHIdentity(id.Identity{Role: "admin"}); err == nil {
		t.Fatal("identity without subject must be denied")
	}
	owner, err := authorizeSSHIdentity(id.Identity{Role: "admin", Subject: "admin-1"})
	if err != nil || owner != "admin-1" {
		t.Fatalf("admin identity rejected: owner=%q err=%v", owner, err)
	}
}

func TestSessionForOwnerRejectsCrossUserAccess(t *testing.T) {
	a := &Actor{sessions: map[string]*session{"session-1": {id: "session-1", owner: "user-1"}}}
	if _, err := a.sessionForOwner("user-2", "session-1"); err == nil {
		t.Fatal("cross-user session access must be denied")
	}
	if _, err := a.sessionForOwner("user-1", "session-1"); err != nil {
		t.Fatalf("owner session access rejected: %v", err)
	}
}

func TestLoadMigratesInlineCredentials(t *testing.T) {
	store := persist.NewFSPersist(t.TempDir())
	legacy := managerSnapshot{Hosts: []domain.SshHost{{ID: "host-1", Name: "legacy", Password: "secret", KeyData: "private-key"}}}
	if err := store.Save("sshmanager", legacy); err != nil {
		t.Fatal(err)
	}
	a := &Actor{store: store}
	if err := a.Load(); err != nil {
		t.Fatal(err)
	}
	if a.Hosts[0].Password != "" || a.Hosts[0].KeyData != "" || !a.hasCredential("host-1") {
		t.Fatalf("inline credentials were not migrated: host=%+v credentials=%+v", a.Hosts[0], a.Credentials)
	}
	var saved managerSnapshot
	if err := persist.LoadOrZero(store, "sshmanager", &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Hosts[0].Password != "" || saved.Hosts[0].KeyData != "" || saved.Credentials["host-1"].Password != "secret" {
		t.Fatalf("unexpected migrated snapshot: %+v", saved)
	}
}

func TestSshHostViewRedactsCredentials(t *testing.T) {
	view := sshHostView(domain.SshHost{ID: "host-1", Name: "prod", Host: "example.com", Port: 22, User: "root", AuthMethod: "password"}, true)
	data, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	payload := string(data)
	for _, secret := range []string{"secret", "private-key", "/secret/key", "Password", "KeyData", "KeyPath"} {
		if strings.Contains(payload, secret) {
			t.Fatalf("credential leaked in host view: %s", payload)
		}
	}
	if !view.HasCredential {
		t.Fatal("view should report configured credential")
	}
}

func TestRequireSSHToolCallerAllowsAgentRoles(t *testing.T) {
	ctx := &testutil.FakeCtx{}
	for _, tc := range []struct {
		role    id.Role
		subject string
		wantErr bool
		wantSub string
	}{
		{"", "", false, "agent"},
		{"system", "", false, "agent"},
		{"agent", "", false, "agent"},
		{"developer", "user-1", false, "user-1"},
		{"admin", "admin-1", false, "admin-1"},
		{"anonymous", "", true, ""},
	} {
		ctx.Identity_ = id.Identity{Role: tc.role, Subject: tc.subject}
		owner, err := requireSSHToolCaller(ctx)
		if tc.wantErr {
			if err == nil {
				t.Errorf("role %q: expected error, got owner %q", tc.role, owner)
			}
			continue
		}
		if err != nil {
			t.Errorf("role %q: unexpected error: %v", tc.role, err)
			continue
		}
		if owner != tc.wantSub {
			t.Errorf("role %q: owner = %q, want %q", tc.role, owner, tc.wantSub)
		}
	}
}

func TestSessionForIDFindsSessionWithoutOwnerCheck(t *testing.T) {
	a := &Actor{sessions: map[string]*session{
		"sess-1": {id: "sess-1", owner: "agent"},
		"sess-2": {id: "sess-2", owner: "human-1"},
	}}
	sess, err := a.sessionForID("sess-1")
	if err != nil || sess == nil {
		t.Fatalf("sessionForID(sess-1) failed: %v", err)
	}
	if sess.id != "sess-1" {
		t.Fatalf("wrong session: got %q", sess.id)
	}
	// Cross-owner lookup must succeed — no owner match required.
	sess2, err := a.sessionForID("sess-2")
	if err != nil || sess2 == nil || sess2.id != "sess-2" {
		t.Fatalf("sessionForID(sess-2) should find session owned by different caller: %v", err)
	}
	if _, err := a.sessionForID("nonexistent"); err == nil {
		t.Fatal("sessionForID should fail for unknown session ID")
	}
}

func TestToolSessionAuthenticatesAndLooksUpByID(t *testing.T) {
	a := &Actor{sessions: map[string]*session{"sess-1": {id: "sess-1", owner: "agent"}}}
	ctx := &testutil.FakeCtx{Identity_: id.Identity{Role: "agent"}}
	sess, err := a.toolSession(ctx, "sess-1")
	if err != nil || sess == nil {
		t.Fatalf("toolSession failed for agent role: %v", err)
	}
	// Admin caller should also access the agent-opened session.
	adminCtx := &testutil.FakeCtx{Identity_: id.Identity{Role: "admin", Subject: "user-1"}}
	sess2, err := a.toolSession(adminCtx, "sess-1")
	if err != nil || sess2 == nil {
		t.Fatalf("toolSession failed for admin role on agent-opened session: %v", err)
	}
	// Anonymous role denied.
	anonCtx := &testutil.FakeCtx{Identity_: id.Identity{Role: "anonymous"}}
	if _, err := a.toolSession(anonCtx, "sess-1"); err == nil {
		t.Fatal("toolSession should deny anonymous role")
	}
}

// TestHandleHostListRedactsAddressForAgent pins the rule that agent callers
// only ever see host names — never the saved IP/port/user. Humans keep the
// full view because the frontend terminal and FTP panels display it.
func TestHandleHostListRedactsAddressForAgent(t *testing.T) {
	a := &Actor{
		store:       persist.NewFSPersist(t.TempDir()),
		Credentials: make(map[string]sshCredential),
		Hosts: []domain.SshHost{{
			ID: "host-1", Name: "prod", Host: "10.0.0.5", Port: 2222,
			User: "root", AuthMethod: "password", Group: "prod",
		}},
	}
	a.Credentials["host-1"] = sshCredential{Password: "secret"}

	agent, err := a.handleHostList(agentCtx(), domain.SshHostListReq{})
	if err != nil {
		t.Fatalf("agent host_list rejected: %v", err)
	}
	v := agent.Items[0]
	if v.Host != "" || v.Port != 0 || v.User != "" || v.AuthMethod != "" {
		t.Fatalf("connection details leaked to agent caller: %+v", v)
	}
	if v.ID != "host-1" || v.Name != "prod" || v.Group != "prod" || !v.HasCredential {
		t.Fatalf("name/id surface must survive redaction for operations: %+v", v)
	}

	full, err := a.handleHostList(adminCtx(), domain.SshHostListReq{})
	if err != nil {
		t.Fatalf("admin host_list rejected: %v", err)
	}
	fv := full.Items[0]
	if fv.Host != "10.0.0.5" || fv.Port != 2222 || fv.User != "root" {
		t.Fatalf("human caller must keep the full view for the frontend UI: %+v", fv)
	}
}

// TestHandleSessionListRedactsAddressForAgent mirrors the host_list rule for
// open sessions: agents match sessions by HostId/HostName to reuse them, so
// HostAddr/User are not needed and must not leak.
func TestHandleSessionListRedactsAddressForAgent(t *testing.T) {
	a := &Actor{sessions: map[string]*session{"sess-1": {
		id: "sess-1", hostID: "host-1", hostName: "prod",
		hostAddr: "10.0.0.5", user: "root", owner: "agent",
		connected: true, cwd: "/srv",
	}}}

	agent, err := a.handleSessionList(agentCtx(), domain.SshSessionListReq{})
	if err != nil {
		t.Fatalf("agent session_list rejected: %v", err)
	}
	info := agent.Items[0]
	if info.HostAddr != "" || info.User != "" {
		t.Fatalf("connection details leaked in session list to agent caller: %+v", info)
	}
	if info.SessionID != "sess-1" || info.HostID != "host-1" || info.HostName != "prod" ||
		info.Cwd != "/srv" {
		t.Fatalf("session identity surface must survive redaction: %+v", info)
	}

	full, err := a.handleSessionList(adminCtx(), domain.SshSessionListReq{})
	if err != nil {
		t.Fatalf("admin session_list rejected: %v", err)
	}
	fInfo := full.Items[0]
	if fInfo.HostAddr != "10.0.0.5" || fInfo.User != "root" {
		t.Fatalf("human caller must keep the full session view: %+v", fInfo)
	}
}

// newInvisibleTestActor builds an actor with one visible and one
// AgentInvisible host plus one session each, both opened by a human.
func newInvisibleTestActor(t *testing.T) *Actor {
	t.Helper()
	a := &Actor{
		store:       persist.NewFSPersist(t.TempDir()),
		Credentials: make(map[string]sshCredential),
		Hosts: []domain.SshHost{
			{ID: "host-vis", Name: "visible", Host: "127.0.0.1", Port: 1, User: "root", AuthMethod: "password"},
			{ID: "host-hid", Name: "hidden", Host: "127.0.0.1", Port: 1, User: "root", AuthMethod: "password", AgentInvisible: true},
		},
		sessions: map[string]*session{
			"sess-vis": {id: "sess-vis", hostID: "host-vis", hostName: "visible", owner: "user-1", connected: true},
			"sess-hid": {id: "sess-hid", hostID: "host-hid", hostName: "hidden", owner: "user-1", connected: true},
		},
	}
	a.Credentials["host-vis"] = sshCredential{Password: "pw"}
	a.Credentials["host-hid"] = sshCredential{Password: "pw"}
	return a
}

// TestAgentInvisibleHostHiddenFromLists pins the AgentInvisible rule on the
// list callables: agent callers must not see the host in host_list, must not
// see its sessions in session_list, and must not get a status entry in
// status_list. Human callers keep everything.
func TestAgentInvisibleHostHiddenFromLists(t *testing.T) {
	a := newInvisibleTestActor(t)

	agentHosts, err := a.handleHostList(agentCtx(), domain.SshHostListReq{})
	if err != nil {
		t.Fatalf("agent host_list rejected: %v", err)
	}
	if len(agentHosts.Items) != 1 || agentHosts.Items[0].ID != "host-vis" {
		t.Fatalf("agent host_list must exclude the invisible host: %+v", agentHosts.Items)
	}

	humanHosts, err := a.handleHostList(adminCtx(), domain.SshHostListReq{})
	if err != nil {
		t.Fatalf("admin host_list rejected: %v", err)
	}
	if len(humanHosts.Items) != 2 {
		t.Fatalf("human host_list must include the invisible host: %+v", humanHosts.Items)
	}
	for _, v := range humanHosts.Items {
		if v.ID == "host-hid" && !v.AgentInvisible {
			t.Fatalf("host view must report AgentInvisible: %+v", v)
		}
	}

	agentSessions, err := a.handleSessionList(agentCtx(), domain.SshSessionListReq{})
	if err != nil {
		t.Fatalf("agent session_list rejected: %v", err)
	}
	if len(agentSessions.Items) != 1 || agentSessions.Items[0].SessionID != "sess-vis" {
		t.Fatalf("agent session_list must exclude sessions of invisible hosts: %+v", agentSessions.Items)
	}
	humanSessions, err := a.handleSessionList(adminCtx(), domain.SshSessionListReq{})
	if err != nil {
		t.Fatalf("admin session_list rejected: %v", err)
	}
	if len(humanSessions.Items) != 2 {
		t.Fatalf("human session_list must keep sessions of invisible hosts: %+v", humanSessions.Items)
	}
}

// TestAgentInvisibleHostRejectsAgentOperations pins the operate side: for
// agent callers every host-scoped entry point (status_get, shell_open, exec)
// and every session-scoped entry point (toolSession) returns not-found for
// an invisible host, without revealing that the host exists.
func TestAgentInvisibleHostRejectsAgentOperations(t *testing.T) {
	a := newInvisibleTestActor(t)

	if _, err := a.handleStatusGet(agentCtx(), domain.SshStatusReq{HostID: "host-hid"}); err == nil {
		t.Fatal("agent status_get on invisible host must fail")
	}
	if _, err := a.handleShellOpen(agentCtx(), domain.SshShellOpenReq{HostID: "host-hid"}); err == nil {
		t.Fatal("agent shell_open on invisible host must fail")
	}
	if _, err := a.handleExec(agentCtx(), domain.SshExecReq{HostID: "host-hid", Command: "true"}); err == nil {
		t.Fatal("agent exec on invisible host must fail")
	}
	if _, err := a.toolSession(agentCtx(), "sess-hid"); err == nil {
		t.Fatal("agent toolSession on a session of an invisible host must fail")
	}
	// Human callers keep operating the same host and session.
	human := adminCtx()
	if _, err := a.toolSession(human, "sess-hid"); err != nil {
		t.Fatalf("human toolSession on invisible-host session must succeed: %v", err)
	}
}

// TestHostCRUDRoundTripsAgentInvisible pins persistence of the flag through
// create and update: a host created invisible stays invisible, and an update
// can toggle the flag.
func TestHostCRUDRoundTripsAgentInvisible(t *testing.T) {
	a := &Actor{
		store:       persist.NewFSPersist(t.TempDir()),
		Credentials: make(map[string]sshCredential),
	}
	created, err := a.handleHostCreate(adminCtx(), domain.SshHostCreateReq{
		Name: "prod", Host: "10.0.0.9", Port: 22, User: "root", AuthMethod: "password",
		AgentInvisible: true, Password: "pw",
	})
	if err != nil {
		t.Fatalf("host_create rejected: %v", err)
	}
	if !created.Host.AgentInvisible || !a.Hosts[0].AgentInvisible {
		t.Fatalf("created host must carry AgentInvisible: %+v", created.Host)
	}

	if _, err := a.handleHostUpdate(adminCtx(), domain.SshHostUpdateReq{
		ID: created.Host.ID, Name: "prod", Host: "10.0.0.9", Port: 22, User: "root",
		AuthMethod: "password", AgentInvisible: false,
	}); err != nil {
		t.Fatalf("host_update rejected: %v", err)
	}
	if a.Hosts[0].AgentInvisible {
		t.Fatal("host_update must be able to clear AgentInvisible")
	}
}
