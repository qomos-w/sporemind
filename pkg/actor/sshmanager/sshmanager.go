// Package sshmanager manages remote SSH hosts, interactive shell sessions,
// SFTP file operations, and remote status monitoring.
//
// Topology:
//
//	/sshmanager                # this actor; holds []SshHost
//	├── /sshmanager/session-1  # one interactive shell session per child
//	└── ...
//
// Hosts are persisted. Sessions are ephemeral (in-memory only).
package sshmanager

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/pkg/sftp"
	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/policy"
	"github.com/skeema/knownhosts"
	"golang.org/x/crypto/ssh"
)

type sshCredential struct {
	Password string `json:"password,omitempty"`
	KeyPath  string `json:"keyPath,omitempty"`
	KeyData  string `json:"keyData,omitempty"`
}

// Actor manages SSH hosts and sessions.
type Actor struct {
	actor.Host
	store       persist.Persist
	Hosts       []domain.SshHost `gospore:"component,admin"`
	Credentials map[string]sshCredential
	Commands    []domain.SshCommandSnippet
	History     []string
	// Groups holds user-created folder names in display order. Folders exist
	// independently of hosts, so an empty folder persists.
	Groups []string

	mu       sync.Mutex
	sessions map[string]*session // sessionID -> session

	// execConns caches one-shot exec connections keyed by (callerID, hostID).
	// These are independent of the interactive PTY sessions: exec reuses the
	// underlying *ssh.Client but never a PTY channel. Transient — never
	// persisted.
	execConns  map[string]*execConn
	execDial   func(*domain.SshHost) (*ssh.Client, error)
	execCtx    context.Context
	execCancel context.CancelFunc

	// tunnels holds live ssh -L port forwards keyed by (hostID, targetAddr),
	// shared across callers via reference counting. Transient — never
	// persisted; rebuilt on demand after a restart.
	tunnels map[string]*tunnel
}

var _ persist.Persistent = (*Actor)(nil)

type managerSnapshot struct {
	Hosts       []domain.SshHost           `json:"hosts"`
	Credentials map[string]sshCredential   `json:"credentials,omitempty"`
	Commands    []domain.SshCommandSnippet `json:"commands,omitempty"`
	History     []string                   `json:"history,omitempty"`
	Groups      []string                   `json:"groups,omitempty"`
}

func (a *Actor) OnInit(ctx actor.Context) error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("sshmanager"))
		if err != nil {
			return err
		}
	}
	a.sessions = make(map[string]*session)
	a.execConns = make(map[string]*execConn)
	a.tunnels = make(map[string]*tunnel)
	a.Credentials = make(map[string]sshCredential)
	if err := a.Load(); err != nil {
		ctx.Logger().Error("sshmanager: load state failed", "error", err)
	}
	return nil
}

func (a *Actor) Type() string { return "sshmanager" }

func (a *Actor) OnStart(ctx actor.Context) error {
	ctx.Logger().Info("sshmanager: starting", "hosts", len(a.Hosts))

	if err := ctx.Register("sshmanager.host_list", a.handleHostList, actor.AdminOnly(),
		actor.WithDescription("List saved SSH hosts (id, name, address, user, auth mode, groups). Non-admin callers see a redacted view (connection details blanked, agent-invisible hosts omitted). Resolve HostId here before shell/file operations."),
	); err != nil {
		return fmt.Errorf("sshmanager: register host.list: %w", err)
	}
	if err := ctx.Register("sshmanager.host_create", a.handleHostCreate, actor.AdminOnly(),
	); err != nil {
		return fmt.Errorf("sshmanager: register host.create: %w", err)
	}
	if err := ctx.Register("sshmanager.host_update", a.handleHostUpdate, actor.AdminOnly(),
	); err != nil {
		return fmt.Errorf("sshmanager: register host.update: %w", err)
	}
	if err := ctx.Register("sshmanager.host_remove", a.handleHostRemove, actor.AdminOnly(),
	); err != nil {
		return fmt.Errorf("sshmanager: register host.remove: %w", err)
	}
	if err := ctx.Register("sshmanager.folder_create", a.handleFolderCreate, actor.AdminOnly(),
	); err != nil {
		return fmt.Errorf("sshmanager: register folder.create: %w", err)
	}
	if err := ctx.Register("sshmanager.folder_rename", a.handleFolderRename, actor.AdminOnly(),
	); err != nil {
		return fmt.Errorf("sshmanager: register folder.rename: %w", err)
	}
	if err := ctx.Register("sshmanager.folder_reorder", a.handleFolderReorder, actor.AdminOnly(),
	); err != nil {
		return fmt.Errorf("sshmanager: register folder.reorder: %w", err)
	}
	if err := ctx.Register("sshmanager.folder_remove", a.handleFolderRemove, actor.AdminOnly(),
	); err != nil {
		return fmt.Errorf("sshmanager: register folder.remove: %w", err)
	}
	if err := ctx.Register("sshmanager.session_list", a.handleSessionList, actor.AdminOnly(),
		actor.WithDescription(`List all currently open interactive SSH shell sessions. Call this BEFORE sshmanager.shell_open to find an existing session for the target host — reuse its sessionId with sshmanager.shell_run instead of opening a duplicate. Returns each session's SessionID, HostID, HostName, Connected, and Cwd.`),
	); err != nil {
		return fmt.Errorf("sshmanager: register session.list: %w", err)
	}
	if err := ctx.Register("sshmanager.shell_open", a.handleShellOpen, actor.AdminOnly(),
		actor.WithDescription(`Open a new interactive PTY session on a remote host. Before calling this, check sshmanager.session_list for an existing session on the same host and reuse it with sshmanager.shell_run — do not open duplicate sessions. Returns SessionID and Connected.`),
	); err != nil {
		return fmt.Errorf("sshmanager: register shell.open: %w", err)
	}
	if err := ctx.Register("sshmanager.shell_close", a.handleShellClose, actor.AdminOnly(),
	); err != nil {
		return fmt.Errorf("sshmanager: register shell.close: %w", err)
	}
	if err := ctx.Register("sshmanager.shell_input", a.handleShellInput, actor.AdminOnly(),
	); err != nil {
		return fmt.Errorf("sshmanager: register shell.input: %w", err)
	}
	if err := ctx.Register("sshmanager.shell_resize", a.handleShellResize, actor.AdminOnly(),
	); err != nil {
		return fmt.Errorf("sshmanager: register shell.resize: %w", err)
	}
	if err := ctx.Register("sshmanager.shell_stream", a.handleShellStream,
		actor.AdminOnly(),
		actor.Streaming[domain.SshShellStreamChunk](),
		actor.WithDescription("Raw PTY output stream for the frontend terminal (xterm.js); one chunk per output burst, final chunk Exited=true"),
	); err != nil {
		return fmt.Errorf("sshmanager: register shell.stream: %w", err)
	}
	if err := ctx.Register("sshmanager.file_list", a.handleFileList, actor.AdminOnly(),
	); err != nil {
		return fmt.Errorf("sshmanager: register file.list: %w", err)
	}
	if err := ctx.Register("sshmanager.file_read", a.handleFileRead, actor.AdminOnly(),
	); err != nil {
		return fmt.Errorf("sshmanager: register file.read: %w", err)
	}
	if err := ctx.Register("sshmanager.file_write", a.handleFileWrite, actor.AdminOnly(),
	); err != nil {
		return fmt.Errorf("sshmanager: register file.write: %w", err)
	}
	if err := ctx.Register("sshmanager.file_write_base64", a.handleFileWriteBase64, actor.AdminOnly(),
		actor.WithDescription("Decode base64 content and overwrite a remote file in the session"),
	); err != nil {
		return fmt.Errorf("sshmanager: register file.write_base64: %w", err)
	}
	if err := ctx.Register("sshmanager.file_mkdir", a.handleFileMkdir, actor.AdminOnly(),
	); err != nil {
		return fmt.Errorf("sshmanager: register file.mkdir: %w", err)
	}
	if err := ctx.Register("sshmanager.file_delete", a.handleFileDelete, actor.AdminOnly(),
	); err != nil {
		return fmt.Errorf("sshmanager: register file.delete: %w", err)
	}
	if err := ctx.Register("sshmanager.file_rename", a.handleFileRename, actor.AdminOnly(),
	); err != nil {
		return fmt.Errorf("sshmanager: register file.rename: %w", err)
	}
	if err := ctx.Register("sshmanager.file_chmod", a.handleFileChmod, actor.AdminOnly(),
	); err != nil {
		return fmt.Errorf("sshmanager: register file.chmod: %w", err)
	}
	if err := ctx.Register("sshmanager.file_download", a.handleFileDownload, actor.AdminOnly(),
	); err != nil {
		return fmt.Errorf("sshmanager: register file.download: %w", err)
	}
	if err := ctx.Register("sshmanager.archive_export", a.handleArchiveExport, actor.AdminOnly(),
		actor.WithDescription("Recursively archive a remote directory into a base64 tar.gz and return it"),
	); err != nil {
		return fmt.Errorf("sshmanager: register archive.export: %w", err)
	}
	if err := ctx.Register("sshmanager.archive_import", a.handleArchiveImport, actor.AdminOnly(),
		actor.WithDescription("Safely extract a base64 tar.gz into a remote directory"),
	); err != nil {
		return fmt.Errorf("sshmanager: register archive.import: %w", err)
	}
	if err := ctx.Register("sshmanager.download", a.handleDownload, actor.AdminOnly(),
		actor.WithDescription(`Download a remote file or directory from an SSH session. Auto-detects file vs directory: a file is returned as base64 bytes; a directory is archived into a tar.gz and returned as base64. The IsDirectory field in the response indicates which path was taken. Requires a sessionId from sshmanager.shell_open or an existing session.`),
		actor.WithParams(
			actor.ParamDesc{Name: "sessionId", Description: "ID of an open shell session (from sshmanager.session_list or sshmanager.shell_open)"},
			actor.ParamDesc{Name: "path", Description: "Remote file or directory path to download"},
		),
	); err != nil {
		return fmt.Errorf("sshmanager: register download: %w", err)
	}
	if err := ctx.Register("sshmanager.upload", a.handleUpload, actor.AdminOnly(),
		actor.WithEffect(string(domain.EffectIrreversible)),
		actor.WithDescription(`Upload base64 content to a remote SSH session. When isArchive is true, the content is decoded as a tar.gz and extracted into the target directory (auto-created if missing). When false, the content is decoded as raw bytes and written to the target file (parent directories auto-created). Requires a sessionId from sshmanager.shell_open or an existing session.`),
		actor.WithParams(
			actor.ParamDesc{Name: "sessionId", Description: "ID of an open shell session (from sshmanager.session_list or sshmanager.shell_open)"},
			actor.ParamDesc{Name: "path", Description: "Remote target path (file for raw upload, directory for archive extract)"},
			actor.ParamDesc{Name: "content", Description: "Base64-encoded file bytes or tar.gz archive bytes"},
			actor.ParamDesc{Name: "isArchive", Description: "true: extract tar.gz into target directory; false: write raw bytes to target file"},
		),
	); err != nil {
		return fmt.Errorf("sshmanager: register upload: %w", err)
	}
	if err := ctx.Register("sshmanager.status_get", a.handleStatusGet, actor.AdminOnly(),
		actor.WithDescription("Connect to one saved SSH host and collect a live system status snapshot: load averages, memory and swap usage, CPU percent, and uptime. Connected=false when the connection fails."),
	); err != nil {
		return fmt.Errorf("sshmanager: register status.get: %w", err)
	}
	if err := ctx.Register("sshmanager.status_list", a.handleStatusList, actor.AdminOnly(),
		actor.WithDescription("Connect to every visible saved SSH host and collect the same system status snapshot per host as status_get (load, memory, swap, CPU, uptime); unreachable hosts report Connected=false."),
	); err != nil {
		return fmt.Errorf("sshmanager: register status.list: %w", err)
	}
	if err := ctx.Register("sshmanager.command_list", a.handleCommandList, actor.AdminOnly(),
	); err != nil {
		return fmt.Errorf("sshmanager: register command.list: %w", err)
	}
	if err := ctx.Register("sshmanager.command_create", a.handleCommandCreate, actor.AdminOnly(),
	); err != nil {
		return fmt.Errorf("sshmanager: register command.create: %w", err)
	}
	if err := ctx.Register("sshmanager.command_update", a.handleCommandUpdate, actor.AdminOnly(),
	); err != nil {
		return fmt.Errorf("sshmanager: register command.update: %w", err)
	}
	if err := ctx.Register("sshmanager.command_remove", a.handleCommandRemove, actor.AdminOnly(),
	); err != nil {
		return fmt.Errorf("sshmanager: register command.remove: %w", err)
	}
	if err := ctx.Register("sshmanager.history_list", a.handleHistoryList, actor.AdminOnly(),
	); err != nil {
		return fmt.Errorf("sshmanager: register history.list: %w", err)
	}
	if err := ctx.Register("sshmanager.exec", a.handleExec, actor.AdminOnly(),
		actor.WithEffect(string(domain.EffectIrreversible)),
		actor.WithDescription(`Run a command on a remote SSH host and return stdout/stderr/exit code. Connections are cached per (caller, host) and reused across calls; a dropped connection is re-established once. Timeout is in milliseconds; default 30000 (30s). Values below 1000 are clamped to the default; larger values are allowed.`),
		actor.WithParams(
			actor.ParamDesc{Name: "hostId", Description: "ID of the saved SSH host to run the command on"},
			actor.ParamDesc{Name: "command", Description: "Shell command to execute on the remote host"},
			actor.ParamDesc{Name: "timeout", Description: "Timeout in milliseconds; default 30000 (30s); values below 1000 are clamped to default; larger values are allowed"},
		),
	); err != nil {
		return fmt.Errorf("sshmanager: register exec: %w", err)
	}
	if err := ctx.Register("sshmanager.shell_run", a.handleShellRun, actor.AdminOnly(),
		actor.WithEffect(string(domain.EffectIrreversible)),
		actor.WithDescription(`Run a command on an existing interactive SSH shell session and return the text output with exit code. Call sshmanager.session_list first to find an existing session for the target host; if none exists, open one with sshmanager.shell_open. Reuse existing sessions — do not open duplicates. Environment state (cwd, env vars, venv) is preserved across calls. A sentinel marker is appended to detect completion and capture the exit code. Not suitable for non-terminating commands (tail -f, top). TimeoutMs is in milliseconds; default 30000 (30s). Values below 1000 are clamped to the default.`),
		actor.WithParams(
			actor.ParamDesc{Name: "sessionId", Description: "ID of an open shell session (from sshmanager.session_list or sshmanager.shell_open)"},
			actor.ParamDesc{Name: "command", Description: "Shell command to execute in the session"},
			actor.ParamDesc{Name: "timeoutMs", Description: "Timeout in milliseconds; default 30000 (30s); values below 1000 are clamped to default; larger values are allowed"},
		),
	); err != nil {
		return fmt.Errorf("sshmanager: register shell.run: %w", err)
	}
	if err := ctx.Register("sshmanager.tunnel_open", a.handleTunnelOpen, actor.AdminOnly(),
		actor.WithEffect(string(domain.EffectIrreversible)),
		actor.WithDescription(`Open a local port forward (ssh -L semantics) to a target address reachable from a saved SSH host, and return the local address to dial. TargetAddr is host:port as seen from the SSH host — use "127.0.0.1:6379" to reach a service on the host's own loopback. Dial LocalAddr with any normal TCP client (redis, postgres, HTTP, …); no SSH is needed on the caller side. Tunnels are shared per (host, target): opening the same target again just adds a reference — every open must be matched by exactly one tunnel_close. Tunnels are not persisted; reopen them after a restart.`),
		actor.WithParams(
			actor.ParamDesc{Name: "hostId", Description: "ID of the saved SSH host to forward through"},
			actor.ParamDesc{Name: "targetAddr", Description: "host:port reachable from the SSH host, e.g. \"127.0.0.1:6379\""},
		),
	); err != nil {
		return fmt.Errorf("sshmanager: register tunnel.open: %w", err)
	}
	if err := ctx.Register("sshmanager.tunnel_close", a.handleTunnelClose, actor.AdminOnly(),
		actor.WithEffect(string(domain.EffectIrreversible)),
		actor.WithDescription(`Release one reference on a tunnel opened via sshmanager.tunnel_open. When the last reference is released the local listener and SSH connection are torn down. Must be called exactly once per successful tunnel_open.`),
		actor.WithParams(
			actor.ParamDesc{Name: "hostId", Description: "ID of the SSH host the tunnel forwards through"},
			actor.ParamDesc{Name: "targetAddr", Description: "host:port of the tunnel target (same value passed to tunnel_open)"},
		),
	); err != nil {
		return fmt.Errorf("sshmanager: register tunnel.close: %w", err)
	}
	if err := ctx.Register("sshmanager.tunnel_list", a.handleTunnelList, actor.AdminOnly(),
		actor.WithDescription(`List all live ssh -L tunnels: each tunnel's hostId, targetAddr, dialable localAddr, reference count, and in-flight forwarded connections. Use before opening a new tunnel to check whether the target is already forwarded.`),
	); err != nil {
		return fmt.Errorf("sshmanager: register tunnel.list: %w", err)
	}

	// Default exec dialer (overridable in tests).
	if a.execDial == nil {
		a.execDial = buildSSHClient
	}
	// Idle reclamation: close cached exec connections unused for execIdleTimeout.
	if a.execCtx == nil {
		a.execCtx, a.execCancel = context.WithCancel(context.Background())
		go a.reclaimIdleExecConns()
	}

	_ = ctx.RegisterEventKind("ssh_manager_event", domain.SshManagerEvent{}, actor.Public())

	// Process-wide tunnel resolver for persist backends (B5): a database
	// backend with PersistConfig.TunnelRef dials through our ssh -L forwards.
	// Installed at startup, like dbmanager's credential resolver; replaced on
	// restart, never silently absent when a backend requests a tunnel.
	persist.SetTunnelResolver(a.resolvePersistTunnel)

	if err := ctx.RegisterDomain("sshmanager").Expose(); err != nil {
		return fmt.Errorf("sshmanager: expose: %w", err)
	}

	return nil
}

// OnStop tears down transient exec connection cache and the idle reclamation
// goroutine. Exec connections are never persisted; they are closed here.
func (a *Actor) OnStop(_ actor.Context) error {
	if a.execCancel != nil {
		a.execCancel()
	}
	a.mu.Lock()
	conns := a.execConns
	a.execConns = make(map[string]*execConn)
	a.mu.Unlock()
	for _, ec := range conns {
		if ec.client != nil {
			_ = ec.client.Close()
		}
	}
	a.closeAllTunnels()
	return nil
}

// Save persists the actor's durable state.
func (a *Actor) Save() error {
	snap := managerSnapshot{
		Hosts:       a.Hosts,
		Credentials: a.Credentials,
		Commands:    a.Commands,
		History:     a.History,
		Groups:      a.Groups,
	}
	return a.store.Save("sshmanager", snap)
}

// Load restores the actor's durable state.
func (a *Actor) Load() error {
	var snap managerSnapshot
	if err := persist.LoadOrZero(a.store, "sshmanager", &snap); err != nil {
		return err
	}
	a.Hosts = snap.Hosts
	a.Credentials = snap.Credentials
	a.Commands = snap.Commands
	a.History = snap.History
	a.Groups = snap.Groups
	if a.Credentials == nil {
		a.Credentials = make(map[string]sshCredential)
	}
	migrated := false
	for i := range a.Hosts {
		host := &a.Hosts[i]
		if host.Password == "" && host.KeyPath == "" && host.KeyData == "" {
			continue
		}
		a.Credentials[host.ID] = sshCredential{Password: host.Password, KeyPath: host.KeyPath, KeyData: host.KeyData}
		host.Password, host.KeyPath, host.KeyData = "", "", ""
		migrated = true
	}
	if migrated {
		return a.Save()
	}
	return nil
}

// ---------------------------------------------------------------------------
// Host helpers
// ---------------------------------------------------------------------------

func (a *Actor) hasCredential(hostID string) bool {
	credential, ok := a.Credentials[hostID]
	return ok && (credential.Password != "" || credential.KeyPath != "" || credential.KeyData != "")
}

func (a *Actor) hostWithCredential(host *domain.SshHost) domain.SshHost {
	resolved := *host
	credential := a.Credentials[host.ID]
	resolved.Password = credential.Password
	resolved.KeyPath = credential.KeyPath
	resolved.KeyData = credential.KeyData
	return resolved
}

func (a *Actor) findHost(id string) *domain.SshHost {
	for i := range a.Hosts {
		if a.Hosts[i].ID == id {
			return &a.Hosts[i]
		}
	}
	return nil
}

func (a *Actor) removeHost(id string) bool {
	for i, h := range a.Hosts {
		if h.ID == id {
			a.Hosts = append(a.Hosts[:i], a.Hosts[i+1:]...)
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// SSH connection helpers
// ---------------------------------------------------------------------------

// Strict host key verification is an explicit opt-in (tunnels carry DB
// traffic, so operators that want verification must be able to demand it):
//
//	SPOREMIND_SSH_STRICT_HOSTKEY=1            verify against the known_hosts file
//	SPOREMIND_SSH_STRICT_HOSTKEY=1
//	SPOREMIND_SSH_KNOWN_HOSTS=<path>          ...at an explicit path
//
// Without the env flag the behavior is unchanged (InsecureIgnoreHostKey) so
// existing saved hosts and in-process test servers keep working; the flag is
// environment policy, not a per-host silent default.
func hostKeyCallback() (ssh.HostKeyCallback, error) {
	if os.Getenv("SPOREMIND_SSH_STRICT_HOSTKEY") != "1" {
		return ssh.InsecureIgnoreHostKey(), nil
	}
	path := os.Getenv("SPOREMIND_SSH_KNOWN_HOSTS")
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("strict host key: resolve home dir: %w", err)
		}
		path = filepath.Join(home, ".ssh", "known_hosts")
	}
	cb, err := knownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf("strict host key: knownhosts %s: %w", path, err)
	}
	return ssh.HostKeyCallback(cb), nil
}

func buildSSHClient(host *domain.SshHost) (*ssh.Client, error) {
	user := host.User
	if user == "" {
		user = "root"
	}
	hostKeyCB, err := hostKeyCallback()
	if err != nil {
		return nil, err
	}
	config := &ssh.ClientConfig{
		User:            user,
		HostKeyCallback: hostKeyCB,
		Timeout:         15 * time.Second,
	}

	switch host.AuthMethod {
	case "password":
		config.Auth = append(config.Auth, ssh.Password(host.Password))
	case "key":
		var keyData []byte
		if host.KeyData != "" {
			keyData = []byte(host.KeyData)
		} else if host.KeyPath != "" {
			var err error
			keyData, err = os.ReadFile(host.KeyPath)
			if err != nil {
				return nil, fmt.Errorf("read key file: %w", err)
			}
		}
		if len(keyData) == 0 {
			return nil, fmt.Errorf("no private key provided")
		}
		signer, err := ssh.ParsePrivateKey(keyData)
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		config.Auth = append(config.Auth, ssh.PublicKeys(signer))
	default:
		return nil, fmt.Errorf("unsupported auth method: %s", host.AuthMethod)
	}

	addr := fmt.Sprintf("%s:%d", host.Host, host.Port)
	if host.Port == 0 {
		addr = host.Host + ":22"
	}
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, fmt.Errorf("ssh dial: %w", err)
	}
	return client, nil
}

// ---------------------------------------------------------------------------
// Session
// ---------------------------------------------------------------------------

type session struct {
	id        string
	hostID    string
	hostName  string
	hostAddr  string
	user      string
	owner     string
	client    *ssh.Client
	shell     *ssh.Session
	stdin     io.WriteCloser
	out       *shellBroadcaster
	mu        sync.Mutex
	runMu     sync.Mutex // serializes shell_run calls on the same session
	connected bool
	cwd       string
	rows      int
	cols      int
	lineBuf   string // accumulates partial PTY input for command-history capture
}

func (s *session) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connected = false
	if s.out != nil {
		s.out.close()
	}
	if s.shell != nil {
		s.shell.Close()
		s.shell = nil
	}
	if s.client != nil {
		s.client.Close()
		s.client = nil
	}
}

func (s *session) isConnected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected && s.client != nil
}

// ---------------------------------------------------------------------------
// Host handlers
// ---------------------------------------------------------------------------

func sshHostView(host domain.SshHost, hasCredential bool) domain.SshHostView {
	return domain.SshHostView{
		ID: host.ID, Name: host.Name, Host: host.Host, Port: host.Port, User: host.User,
		AuthMethod: host.AuthMethod, Group: host.Group, HasCredential: hasCredential,
		AgentInvisible: host.AgentInvisible,
	}
}

func (a *Actor) handleHostList(ctx actor.PureContext, _ domain.SshHostListReq) (domain.SshHostListResp, error) {
	if _, err := requireSSHToolCaller(ctx); err != nil {
		return domain.SshHostListResp{}, err
	}
	redact := !isAdminSSHCaller(ctx)
	items := make([]domain.SshHostView, 0, len(a.Hosts))
	for _, host := range a.Hosts {
		if redact && host.AgentInvisible {
			continue
		}
		v := sshHostView(host, a.hasCredential(host.ID))
		if redact {
			v.Host, v.Port, v.User, v.AuthMethod = "", 0, "", ""
		}
		items = append(items, v)
	}
	return domain.SshHostListResp{Items: items, Groups: a.folderNames()}, nil
}

// folderNames returns the stored folder order with any host-derived groups
// not yet tracked appended in sorted order.
func (a *Actor) folderNames() []string {
	seen := make(map[string]bool, len(a.Groups))
	names := make([]string, 0, len(a.Groups))
	for _, g := range a.Groups {
		if g != "" && !seen[g] {
			seen[g] = true
			names = append(names, g)
		}
	}
	var extra []string
	for _, h := range a.Hosts {
		if h.Group != "" && !seen[h.Group] {
			seen[h.Group] = true
			extra = append(extra, h.Group)
		}
	}
	sort.Strings(extra)
	return append(names, extra...)
}

func (a *Actor) handleFolderCreate(ctx actor.PureContext, req domain.SshFolderCreateReq) (domain.SshFolderCreateResp, error) {
	if _, err := requireSSHCaller(ctx); err != nil {
		return domain.SshFolderCreateResp{}, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return domain.SshFolderCreateResp{}, fmt.Errorf("folder name required")
	}
	for _, g := range a.folderNames() {
		if g == name {
			return domain.SshFolderCreateResp{}, fmt.Errorf("folder %q already exists", name)
		}
	}
	a.Groups = append(a.Groups, name)
	if err := a.Save(); err != nil {
		return domain.SshFolderCreateResp{}, fmt.Errorf("save failed: %w", err)
	}
	return domain.SshFolderCreateResp{}, nil
}

func (a *Actor) handleFolderRename(ctx actor.PureContext, req domain.SshFolderRenameReq) (domain.SshFolderRenameResp, error) {
	if _, err := requireSSHCaller(ctx); err != nil {
		return domain.SshFolderRenameResp{}, err
	}
	from := strings.TrimSpace(req.From)
	to := strings.TrimSpace(req.To)
	if from == "" || to == "" {
		return domain.SshFolderRenameResp{}, fmt.Errorf("folder name required")
	}
	found := false
	for i, g := range a.Groups {
		if g == from {
			a.Groups[i] = to
			found = true
		}
	}
	hostsTouched := false
	for i := range a.Hosts {
		if a.Hosts[i].Group == from {
			a.Hosts[i].Group = to
			hostsTouched = true
		}
	}
	if !found {
		if !hostsTouched {
			return domain.SshFolderRenameResp{}, fmt.Errorf("folder %q not found", from)
		}
		a.Groups = append(a.Groups, to)
	}
	if err := a.Save(); err != nil {
		return domain.SshFolderRenameResp{}, fmt.Errorf("save failed: %w", err)
	}
	return domain.SshFolderRenameResp{}, nil
}

func (a *Actor) handleFolderReorder(ctx actor.PureContext, req domain.SshFolderReorderReq) (domain.SshFolderReorderResp, error) {
	if _, err := requireSSHCaller(ctx); err != nil {
		return domain.SshFolderReorderResp{}, err
	}
	a.Groups = req.Names
	if err := a.Save(); err != nil {
		return domain.SshFolderReorderResp{}, fmt.Errorf("save failed: %w", err)
	}
	return domain.SshFolderReorderResp{}, nil
}

func (a *Actor) handleFolderRemove(ctx actor.PureContext, req domain.SshFolderRemoveReq) (domain.SshFolderRemoveResp, error) {
	if _, err := requireSSHCaller(ctx); err != nil {
		return domain.SshFolderRemoveResp{}, err
	}
	name := strings.TrimSpace(req.Name)
	groups := a.Groups[:0]
	for _, g := range a.Groups {
		if g != name {
			groups = append(groups, g)
		}
	}
	a.Groups = groups
	// Hosts inside the removed folder become ungrouped.
	for i := range a.Hosts {
		if a.Hosts[i].Group == name {
			a.Hosts[i].Group = ""
		}
	}
	if err := a.Save(); err != nil {
		return domain.SshFolderRemoveResp{}, fmt.Errorf("save failed: %w", err)
	}
	return domain.SshFolderRemoveResp{}, nil
}

func (a *Actor) handleHostCreate(ctx actor.PureContext, req domain.SshHostCreateReq) (domain.SshHostCreateResp, error) {
	if _, err := requireSSHCaller(ctx); err != nil {
		return domain.SshHostCreateResp{}, err
	}
	for _, h := range a.Hosts {
		if h.Name == req.Name {
			return domain.SshHostCreateResp{}, fmt.Errorf("host with name %q already exists", req.Name)
		}
	}
	host := domain.SshHost{
		ID:         uuid.New().String(),
		Name:       req.Name,
		Host:       req.Host,
		Port:       req.Port,
		User:       req.User,
		AuthMethod: req.AuthMethod,
		AgentInvisible: req.AgentInvisible,
		Group:      req.Group,
	}
	if host.Port == 0 {
		host.Port = 22
	}
	a.Credentials[host.ID] = sshCredential{Password: req.Password, KeyPath: req.KeyPath, KeyData: req.KeyData}
	a.Hosts = append(a.Hosts, host)
	if err := a.Save(); err != nil {
		return domain.SshHostCreateResp{}, fmt.Errorf("save failed: %w", err)
	}
	return domain.SshHostCreateResp{Host: sshHostView(host, a.hasCredential(host.ID))}, nil
}

func (a *Actor) handleHostUpdate(ctx actor.PureContext, req domain.SshHostUpdateReq) (domain.SshHostUpdateResp, error) {
	if _, err := requireSSHCaller(ctx); err != nil {
		return domain.SshHostUpdateResp{}, err
	}
	h := a.findHost(req.ID)
	if h == nil {
		return domain.SshHostUpdateResp{}, fmt.Errorf("host %q not found", req.ID)
	}
	h.Name = req.Name
	h.Host = req.Host
	h.Port = req.Port
	h.User = req.User
	h.AuthMethod = req.AuthMethod
	h.AgentInvisible = req.AgentInvisible
	h.Group = req.Group
	if req.Password != "" || req.KeyPath != "" || req.KeyData != "" {
		a.Credentials[req.ID] = sshCredential{Password: req.Password, KeyPath: req.KeyPath, KeyData: req.KeyData}
	}
	if h.Port == 0 {
		h.Port = 22
	}
	if err := a.Save(); err != nil {
		return domain.SshHostUpdateResp{}, fmt.Errorf("save failed: %w", err)
	}
	return domain.SshHostUpdateResp{}, nil
}

func (a *Actor) handleHostRemove(ctx actor.PureContext, req domain.SshHostRemoveReq) (domain.SshHostRemoveResp, error) {
	if _, err := requireSSHCaller(ctx); err != nil {
		return domain.SshHostRemoveResp{}, err
	}
	if !a.removeHost(req.ID) {
		return domain.SshHostRemoveResp{}, fmt.Errorf("host %q not found", req.ID)
	}
	delete(a.Credentials, req.ID)
	// Close any active sessions for this host.
	a.mu.Lock()
	for _, s := range a.sessions {
		if s.hostID == req.ID {
			s.close()
		}
	}
	a.mu.Unlock()
	// Tear down any port-forward tunnels bound to this host.
	a.closeTunnelsForHost(req.ID)
	if err := a.Save(); err != nil {
		return domain.SshHostRemoveResp{}, fmt.Errorf("save failed: %w", err)
	}
	return domain.SshHostRemoveResp{}, nil
}

// ---------------------------------------------------------------------------
// Session handlers
// ---------------------------------------------------------------------------

func authorizeSSHIdentity(identity id.Identity) (string, error) {
	if err := policy.RequireAdmin(identity.Role); err != nil {
		return "", err
	}
	if identity.Subject == "" {
		return "", fmt.Errorf("forbidden: SSH caller subject is required")
	}
	return identity.Subject, nil
}

func requireSSHCaller(ctx actor.PureContext) (string, error) {
	return authorizeSSHIdentity(ctx.Identity())
}

// requireSSHToolCaller is the agent-accessible variant of requireSSHCaller.
// It allows human/admin callers and internal agent callers (role "" or
// "system"), following the mcp.call_tool / computeruse.interact precedent.
// Security is enforced by the turn engine's effect-based permission policy
// (WithEffect on exec), not by a human-role gate. The CRUD and lifecycle
// callables stay strictly human-gated via requireSSHCaller.
func requireSSHToolCaller(ctx actor.PureContext) (string, error) {
	identity := ctx.Identity()
	if identity.Role != "" {
		if err := policy.RequireAgentOrHuman(identity.Role); err != nil {
			return "", err
		}
	}
	if identity.Subject == "" {
		return "agent", nil
	}
	return identity.Subject, nil
}

// isAdminSSHCaller reports whether the caller may see saved connection
// details (address, port, user). Agents only get host/session *names* —
// they reference hosts by id/name, which is enough for every operation,
// while the IP stays out of the tool loop and the LLM context.
func isAdminSSHCaller(ctx actor.PureContext) bool {
	return policy.RequireAdmin(ctx.Identity().Role) == nil
}

// hostVisibleToCaller reports whether the caller may see or operate hostID.
// Hosts marked AgentInvisible are fully hidden from agent callers: absent
// from every list, and every host/session-scoped callable returns
// not-found. Admin callers always keep the full view.
func (a *Actor) hostVisibleToCaller(ctx actor.PureContext, hostID string) bool {
	if isAdminSSHCaller(ctx) {
		return true
	}
	h := a.findHost(hostID)
	return h == nil || !h.AgentInvisible
}

func (a *Actor) sessionForOwner(owner, sessionID string) (*session, error) {
	a.mu.Lock()
	sess, ok := a.sessions[sessionID]
	a.mu.Unlock()
	if !ok || sess.owner != owner {
		return nil, fmt.Errorf("session %q not found", sessionID)
	}
	return sess, nil
}

// sessionForID looks up a session by ID without checking ownership. Used by
// toolSession for agent-accessible shell callables (shell_input, shell_stream,
// shell_close, shell_resize) where the session may have been opened by an
// agent but is being interacted with by the human frontend, or vice versa.
func (a *Actor) sessionForID(sessionID string) (*session, error) {
	a.mu.Lock()
	sess, ok := a.sessions[sessionID]
	a.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("session %q not found", sessionID)
	}
	return sess, nil
}

// toolSession authenticates the caller via requireSSHToolCaller (allows human
// and agent roles) and looks up the session by ID without ownership check.
func (a *Actor) toolSession(ctx actor.PureContext, sessionID string) (*session, error) {
	if _, err := requireSSHToolCaller(ctx); err != nil {
		return nil, err
	}
	sess, err := a.sessionForID(sessionID)
	if err != nil {
		return nil, err
	}
	if !a.hostVisibleToCaller(ctx, sess.hostID) {
		return nil, fmt.Errorf("session %q not found", sessionID)
	}
	return sess, nil
}

// emitSshEvent publishes an ssh_manager_event to the app-scoped event bus so
// the desktop frontend can react (e.g. open an ssh-session tab). Mirrors
// browsermanager.emitEvent.
func (a *Actor) emitSshEvent(ctx actor.PureContext, kind string, sess *session) {
	event := domain.SshManagerEvent{
		Kind:      kind,
		SessionID: sess.id,
		HostID:    sess.hostID,
		HostName:  sess.hostName,
	}
	_ = ctx.EmitEvent("ssh_manager_event", event)
}

func (a *Actor) handleSessionList(ctx actor.PureContext, req domain.SshSessionListReq) (domain.SshSessionListResp, error) {
	if _, err := requireSSHToolCaller(ctx); err != nil {
		return domain.SshSessionListResp{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	redact := !isAdminSSHCaller(ctx)
	items := make([]domain.SshSessionInfo, 0, len(a.sessions))
	for _, s := range a.sessions {
		if redact && !a.hostVisibleToCaller(ctx, s.hostID) {
			continue
		}
		info := domain.SshSessionInfo{
			SessionID: s.id,
			HostID:    s.hostID,
			HostName:  s.hostName,
			HostAddr:  s.hostAddr,
			User:      s.user,
			Connected: s.isConnected(),
			Cwd:       s.cwd,
		}
		if redact {
			info.HostAddr, info.User = "", ""
		}
		items = append(items, info)
	}
	return domain.SshSessionListResp{Items: items}, nil
}

func (a *Actor) handleShellOpen(ctx actor.PureContext, req domain.SshShellOpenReq) (domain.SshShellOpenResp, error) {
	owner, err := requireSSHToolCaller(ctx)
	if err != nil {
		return domain.SshShellOpenResp{}, err
	}
	h := a.findHost(req.HostID)
	if h == nil {
		return domain.SshShellOpenResp{}, fmt.Errorf("host %q not found", req.HostID)
	}
	if !a.hostVisibleToCaller(ctx, req.HostID) {
		return domain.SshShellOpenResp{}, fmt.Errorf("host %q not found", req.HostID)
	}

	resolvedHost := a.hostWithCredential(h)
	client, err := buildSSHClient(&resolvedHost)
	if err != nil {
		return domain.SshShellOpenResp{Connected: false, Error: err.Error()}, nil
	}

	shell, err := client.NewSession()
	if err != nil {
		client.Close()
		return domain.SshShellOpenResp{Connected: false, Error: err.Error()}, nil
	}

	modes := ssh.TerminalModes{}
	cols := req.InitialCols
	rows := req.InitialRows
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}
	if err := shell.RequestPty("xterm-256color", int(cols), int(rows), modes); err != nil {
		shell.Close()
		client.Close()
		return domain.SshShellOpenResp{Connected: false, Error: err.Error()}, nil
	}

	stdin, err := shell.StdinPipe()
	if err != nil {
		shell.Close()
		client.Close()
		return domain.SshShellOpenResp{Connected: false, Error: err.Error()}, nil
	}
	out := newShellBroadcaster()
	shell.Stdout = out
	shell.Stderr = out

	if err := shell.Shell(); err != nil {
		shell.Close()
		client.Close()
		return domain.SshShellOpenResp{Connected: false, Error: err.Error()}, nil
	}

	sess := &session{
		id:        uuid.New().String(),
		hostID:    h.ID,
		hostName:  h.Name,
		hostAddr:  fmt.Sprintf("%s:%d", h.Host, h.Port),
		user:      h.User,
		owner:     owner,
		client:    client,
		shell:     shell,
		stdin:     stdin,
		out:       out,
		connected: true,
		rows:      int(rows),
		cols:      int(cols),
	}

	a.mu.Lock()
	a.sessions[sess.id] = sess
	a.mu.Unlock()

	// Start a goroutine to wait for the session to end.
	go func() {
		_ = shell.Wait()
		sess.close()
	}()

	// Keepalive: send a no-op SSH request every 30s to prevent idle
	// disconnection by firewalls or servers with short ClientAliveInterval.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if !sess.isConnected() {
				return
			}
			_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
			if err != nil {
				return
			}
		}
	}()

	a.emitSshEvent(ctx, "open_session", sess)
	return domain.SshShellOpenResp{SessionID: sess.id, Connected: true}, nil
}

func (a *Actor) handleShellClose(ctx actor.PureContext, req domain.SshShellCloseReq) (domain.SshShellCloseResp, error) {
	sess, err := a.toolSession(ctx, req.SessionID)
	if err != nil {
		return domain.SshShellCloseResp{}, err
	}
	a.mu.Lock()
	delete(a.sessions, req.SessionID)
	a.mu.Unlock()
	sess.close()
	// Explicit close (tab close / refresh replacing a stale session) must
	// propagate to every connected client so their tabs for this session
	// disappear; a session dying on its own does NOT emit this so the other
	// clients keep the tab and its reconnect affordance.
	a.emitSshEvent(ctx, "close_session", sess)
	return domain.SshShellCloseResp{}, nil
}

func (a *Actor) handleShellInput(ctx actor.PureContext, req domain.SshShellInputReq) (domain.SshShellInputResp, error) {
	sess, err := a.toolSession(ctx, req.SessionID)
	if err != nil {
		return domain.SshShellInputResp{}, err
	}
	if !sess.isConnected() {
		return domain.SshShellInputResp{}, fmt.Errorf("session %q is not connected", req.SessionID)
	}
	if _, err := sess.stdin.Write([]byte(req.Data)); err != nil {
		return domain.SshShellInputResp{}, fmt.Errorf("write input: %w", err)
	}
	a.recordHistory(sess, req.Data)
	return domain.SshShellInputResp{}, nil
}

func (a *Actor) handleShellResize(ctx actor.PureContext, req domain.SshShellResizeReq) (domain.SshShellResizeResp, error) {
	sess, err := a.toolSession(ctx, req.SessionID)
	if err != nil {
		return domain.SshShellResizeResp{}, err
	}
	if !sess.isConnected() {
		return domain.SshShellResizeResp{}, fmt.Errorf("session %q is not connected", req.SessionID)
	}
	if sess.shell != nil {
		if err := sess.shell.WindowChange(int(req.Rows), int(req.Cols)); err != nil {
			return domain.SshShellResizeResp{}, fmt.Errorf("window change: %w", err)
		}
	}
	sess.mu.Lock()
	sess.rows = int(req.Rows)
	sess.cols = int(req.Cols)
	sess.mu.Unlock()
	return domain.SshShellResizeResp{}, nil
}

// handleShellStream streams raw PTY output to the frontend terminal. The
// handler stays alive for the lifetime of the subscription; chunks carry
// unmodified PTY bytes and the final chunk signals shell exit.
func (a *Actor) handleShellStream(ctx actor.PureContext, req domain.SshShellStreamReq, emit actor.Emitter) error {
	sess, err := a.toolSession(ctx, req.SessionID)
	if err != nil {
		return err
	}
	ch, replay := sess.out.subscribe()
	defer sess.out.unsubscribe(ch)
	// Deliver the replay snapshot (banner/prompt that arrived before this
	// subscription) in original order before reading live data, preserving
	// global PTY byte order without duplicates or reordering.
	for _, chunk := range replay {
		select {
		case <-emit.Done():
			return nil
		default:
		}
		if err := emit.Send(domain.SshShellStreamChunk{Data: chunk, Replay: true}); err != nil {
			return err
		}
	}
	for {
		select {
		case <-emit.Done():
			return nil
		case data, ok := <-ch:
			if !ok {
				_ = emit.Send(domain.SshShellStreamChunk{Exited: true})
				return nil
			}
			if err := emit.Send(domain.SshShellStreamChunk{Data: data}); err != nil {
				return err
			}
		}
	}
}

// ---------------------------------------------------------------------------
// File handlers (SFTP / SCP fallback)
// ---------------------------------------------------------------------------

// getFilesystem returns a remoteFS for the session, trying SFTP first and
// falling back to exec-based SCP commands when the server doesn't support the
// SFTP subsystem.
func (a *Actor) getFilesystem(ctx actor.PureContext, reqSessionID string) (remoteFS, error) {
	sess, err := a.toolSession(ctx, reqSessionID)
	if err != nil {
		return nil, err
	}
	if !sess.isConnected() {
		return nil, fmt.Errorf("session %q is not connected", reqSessionID)
	}
	// Try SFTP first.
	if c, err := sftp.NewClient(sess.client); err == nil {
		return &sftpBackend{raw: c}, nil
	}
	// Fallback: exec-based filesystem.
	return &execBackend{sshClient: sess.client}, nil
}

func (a *Actor) handleFileList(ctx actor.PureContext, req domain.SshFileListReq) (domain.SshFileListResp, error) {
	fs, err := a.getFilesystem(ctx, req.SessionID)
	if err != nil {
		return domain.SshFileListResp{}, err
	}
	defer fs.Close()

	entries, err := fs.List(req.Path)
	if err != nil {
		return domain.SshFileListResp{}, fmt.Errorf("read dir: %w", err)
	}
	return domain.SshFileListResp{Path: req.Path, Entries: entries}, nil
}

func (a *Actor) handleFileRead(ctx actor.PureContext, req domain.SshFileReadReq) (domain.SshFileReadResp, error) {
	fs, err := a.getFilesystem(ctx, req.SessionID)
	if err != nil {
		return domain.SshFileReadResp{}, err
	}
	defer fs.Close()

	data, err := fs.Read(req.Path)
	if err != nil {
		return domain.SshFileReadResp{}, fmt.Errorf("read file: %w", err)
	}

	isBinary := false
	for _, b := range data {
		if b == 0 {
			isBinary = true
			break
		}
	}
	content := string(data)
	if isBinary {
		content = "[binary file]"
	}

	return domain.SshFileReadResp{Path: req.Path, Content: content, IsBinary: isBinary}, nil
}

func (a *Actor) handleFileWrite(ctx actor.PureContext, req domain.SshFileWriteReq) (domain.SshFileWriteResp, error) {
	fs, err := a.getFilesystem(ctx, req.SessionID)
	if err != nil {
		return domain.SshFileWriteResp{}, err
	}
	defer fs.Close()

	if err := fs.Write(req.Path, []byte(req.Content)); err != nil {
		return domain.SshFileWriteResp{}, fmt.Errorf("write file: %w", err)
	}
	return domain.SshFileWriteResp{}, nil
}

// decodeBase64FileContent decodes the base64 payload of a write_base64 request
// so malformed input is rejected before any remote session is opened. Pure so
// it can be unit-tested without an SFTP session.
func decodeBase64FileContent(content string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		return nil, fmt.Errorf("invalid base64 content: %w", err)
	}
	return data, nil
}

// handleFileWriteBase64 overwrites the remote file at req.Path with the bytes
// decoded from req.Content. Remote paths are absolute and scoped to the
// connected session; an empty path is rejected up front.
func (a *Actor) handleFileWriteBase64(ctx actor.PureContext, req domain.SshFileWriteBase64Req) (domain.SshFileWriteBase64Resp, error) {
	if req.Path == "" {
		return domain.SshFileWriteBase64Resp{}, fmt.Errorf("sshmanager.file_write_base64: path is required")
	}
	data, err := decodeBase64FileContent(req.Content)
	if err != nil {
		return domain.SshFileWriteBase64Resp{}, fmt.Errorf("sshmanager.file_write_base64: %w", err)
	}
	fs, err := a.getFilesystem(ctx, req.SessionID)
	if err != nil {
		return domain.SshFileWriteBase64Resp{}, err
	}
	defer fs.Close()

	if err := fs.Write(req.Path, data); err != nil {
		return domain.SshFileWriteBase64Resp{}, fmt.Errorf("write file: %w", err)
	}
	return domain.SshFileWriteBase64Resp{}, nil
}

func (a *Actor) handleFileMkdir(ctx actor.PureContext, req domain.SshFileMkdirReq) (domain.SshFileMkdirResp, error) {
	fs, err := a.getFilesystem(ctx, req.SessionID)
	if err != nil {
		return domain.SshFileMkdirResp{}, err
	}
	defer fs.Close()

	if err := fs.Mkdir(req.Path); err != nil {
		return domain.SshFileMkdirResp{}, fmt.Errorf("mkdir: %w", err)
	}
	return domain.SshFileMkdirResp{}, nil
}

func (a *Actor) handleFileDelete(ctx actor.PureContext, req domain.SshFileDeleteReq) (domain.SshFileDeleteResp, error) {
	fs, err := a.getFilesystem(ctx, req.SessionID)
	if err != nil {
		return domain.SshFileDeleteResp{}, err
	}
	defer fs.Close()

	_, _, isDir, _, _, err := fs.Stat(req.Path)
	if err != nil {
		return domain.SshFileDeleteResp{}, fmt.Errorf("stat: %w", err)
	}
	if isDir {
		if err := fs.RemoveDir(req.Path); err != nil {
			return domain.SshFileDeleteResp{}, fmt.Errorf("remove dir: %w", err)
		}
	} else {
		if err := fs.Remove(req.Path); err != nil {
			return domain.SshFileDeleteResp{}, fmt.Errorf("remove file: %w", err)
		}
	}
	return domain.SshFileDeleteResp{}, nil
}

// removeDirRecursive walks the SFTP directory tree at root and removes every
// file and sub-directory, deepest first, so that RemoveDirectory always
// succeeds on an empty directory. SFTP has no atomic recursive delete, so a
// walker collects paths then deletes in reverse visitation order.
func removeDirRecursive(client *sftp.Client, root string) error {
	var paths []string
	walker := client.Walk(root)
	for walker.Step() {
		if err := walker.Err(); err != nil {
			return err
		}
		paths = append(paths, walker.Path())
	}
	// Delete deepest-first: reverse of BFS/walk order ensures children are
	// removed before their parents.
	for i := len(paths) - 1; i >= 0; i-- {
		p := paths[i]
		info, err := client.Stat(p)
		if err != nil {
			continue // already gone or inaccessible — best effort
		}
		if info.IsDir() {
			if err := client.RemoveDirectory(p); err != nil {
				return fmt.Errorf("remove dir %q: %w", p, err)
			}
		} else {
			if err := client.Remove(p); err != nil {
				return fmt.Errorf("remove file %q: %w", p, err)
			}
		}
	}
	return nil
}

func (a *Actor) handleFileRename(ctx actor.PureContext, req domain.SshFileRenameReq) (domain.SshFileRenameResp, error) {
	fs, err := a.getFilesystem(ctx, req.SessionID)
	if err != nil {
		return domain.SshFileRenameResp{}, err
	}
	defer fs.Close()

	if err := fs.Rename(req.From, req.To); err != nil {
		return domain.SshFileRenameResp{}, fmt.Errorf("rename: %w", err)
	}
	return domain.SshFileRenameResp{}, nil
}

// parseOctalFileMode validates a 3-digit octal permission string ("755",
// "0644") and returns the corresponding os.FileMode. Pure so it can be
// unit-tested without a remote session; the handler rejects anything else
// before opening a session.
func parseOctalFileMode(s string) (os.FileMode, error) {
	if len(s) != 3 {
		return 0, fmt.Errorf("mode must be 3 octal digits, got %q", s)
	}
	for _, c := range s {
		if c < '0' || c > '7' {
			return 0, fmt.Errorf("mode must be 3 octal digits, got %q", s)
		}
	}
	v, _ := strconv.ParseUint(s, 8, 32)
	return os.FileMode(v), nil
}

func (a *Actor) handleFileChmod(ctx actor.PureContext, req domain.SshFileChmodReq) (domain.SshFileChmodResp, error) {
	if req.Path == "" {
		return domain.SshFileChmodResp{}, fmt.Errorf("sshmanager.file_chmod: path is required")
	}
	mode, err := parseOctalFileMode(req.Mode)
	if err != nil {
		return domain.SshFileChmodResp{}, fmt.Errorf("sshmanager.file_chmod: %w", err)
	}
	fs, err := a.getFilesystem(ctx, req.SessionID)
	if err != nil {
		return domain.SshFileChmodResp{}, err
	}
	defer fs.Close()

	if err := fs.Chmod(req.Path, mode); err != nil {
		return domain.SshFileChmodResp{}, fmt.Errorf("chmod: %w", err)
	}
	return domain.SshFileChmodResp{}, nil
}

// maxDownloadBytes caps file download size to protect the JSON transport.
const maxDownloadBytes = 50 * 1024 * 1024

func (a *Actor) handleFileDownload(ctx actor.PureContext, req domain.SshFileDownloadReq) (domain.SshFileDownloadResp, error) {
	fs, err := a.getFilesystem(ctx, req.SessionID)
	if err != nil {
		return domain.SshFileDownloadResp{}, err
	}
	defer fs.Close()

	// Use Stat (follows symlinks) so that a symlink to a directory is
	// correctly detected as a directory. Lstat would report the link
	// itself as a non-directory file, and the subsequent Read would fail
	// with a generic SFTP "Failure" when trying to open the directory.
	name, size, isDir, _, _, err := fs.Stat(req.Path)
	if err != nil {
		return domain.SshFileDownloadResp{}, fmt.Errorf("stat: %w", err)
	}
	if isDir {
		return domain.SshFileDownloadResp{}, fmt.Errorf("cannot download a directory")
	}
	if size > maxDownloadBytes {
		return domain.SshFileDownloadResp{}, fmt.Errorf("file is %d bytes; download limit is %d bytes", size, maxDownloadBytes)
	}

	data, err := fs.Read(req.Path)
	if err != nil {
		return domain.SshFileDownloadResp{}, fmt.Errorf("read file: %w", err)
	}

	return domain.SshFileDownloadResp{
		Name:    name,
		Content: base64.StdEncoding.EncodeToString(data),
		Size:    size,
	}, nil
}

// ---------------------------------------------------------------------------
// Status handlers
// ---------------------------------------------------------------------------

func (a *Actor) handleStatusGet(ctx actor.PureContext, req domain.SshStatusReq) (domain.SshStatusResp, error) {
	if _, err := requireSSHToolCaller(ctx); err != nil {
		return domain.SshStatusResp{}, err
	}
	h := a.findHost(req.HostID)
	if h == nil {
		return domain.SshStatusResp{}, fmt.Errorf("host %q not found", req.HostID)
	}
	if !a.hostVisibleToCaller(ctx, req.HostID) {
		return domain.SshStatusResp{}, fmt.Errorf("host %q not found", req.HostID)
	}

	resolvedHost := a.hostWithCredential(h)
	client, err := buildSSHClient(&resolvedHost)
	if err != nil {
		return domain.SshStatusResp{Status: domain.SshStatus{HostID: req.HostID, HostName: h.Name, Connected: false}}, nil
	}
	defer client.Close()

	status := a.collectStatus(client, h)
	return domain.SshStatusResp{Status: status}, nil
}

func (a *Actor) handleStatusList(ctx actor.PureContext, req domain.SshStatusListReq) (domain.SshStatusListResp, error) {
	if _, err := requireSSHToolCaller(ctx); err != nil {
		return domain.SshStatusListResp{}, err
	}
	items := make([]domain.SshStatus, 0, len(a.Hosts))
	for _, h := range a.Hosts {
		if !a.hostVisibleToCaller(ctx, h.ID) {
			continue
		}
		resolvedHost := a.hostWithCredential(&h)
		client, err := buildSSHClient(&resolvedHost)
		if err != nil {
			items = append(items, domain.SshStatus{HostID: h.ID, HostName: h.Name, Connected: false})
			continue
		}
		status := a.collectStatus(client, &h)
		client.Close()
		items = append(items, status)
	}
	return domain.SshStatusListResp{Items: items}, nil
}

func (a *Actor) collectStatus(client *ssh.Client, h *domain.SshHost) domain.SshStatus {
	status := domain.SshStatus{
		HostID:    h.ID,
		HostName:  h.Name,
		Connected: true,
		Timestamp: time.Now().Format(time.RFC3339),
	}

	// CPU and load average
	out := runSSHCommand(client, "cat /proc/loadavg")
	fields := strings.Fields(out)
	if len(fields) >= 3 {
		for i := 0; i < 3 && i < len(fields); i++ {
			if v, err := strconv.ParseFloat(fields[i], 64); err == nil {
				status.LoadAvg = append(status.LoadAvg, v)
			}
		}
	}

	// Memory
	out = runSSHCommand(client, "cat /proc/meminfo")
	memInfo := parseMemInfo(out)
	status.MemTotal = memInfo["MemTotal"]
	status.MemUsed = memInfo["MemTotal"] - memInfo["MemFree"] - memInfo["Buffers"] - memInfo["Cached"]
	status.SwapTotal = memInfo["SwapTotal"]
	status.SwapUsed = memInfo["SwapTotal"] - memInfo["SwapFree"]

	// CPU percent (real-time via top -bn1, matching r-shell's approach)
	out = runSSHCommand(client, "top -bn1 2>/dev/null | grep 'Cpu(s)' | sed 's/.*, *\\([0-9.]*\\)%* id.*/\\1/' | awk '{print 100 - $1}' || cat /proc/stat | awk '/^cpu /{u=$2+$4; t=$2+$3+$4+$5+$6+$7+$8; printf \"%.1f\\n\", u*100/t}'")
	if v, err := strconv.ParseFloat(strings.TrimSpace(out), 64); err == nil {
		status.CpuPercent = v
	}

	// Uptime
	out = runSSHCommand(client, "awk '{print $1}' /proc/uptime")
	if v, err := strconv.ParseFloat(strings.TrimSpace(out), 64); err == nil {
		status.Uptime = int64(v)
	}

	// Disk usage
	out = runSSHCommand(client, "df -k -P | tail -n +2")
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) >= 6 {
			total, _ := strconv.ParseInt(f[1], 10, 64)
			avail, _ := strconv.ParseInt(f[3], 10, 64)
			status.Disks = append(status.Disks, domain.SshDiskInfo{
				MountPoint: f[5],
				Available:  avail * 1024,
				Total:      total * 1024,
			})
		}
	}

	// Network bandwidth (bytes/sec via dual-sampling /sys/class/net, matching r-shell)
	netCmd := `
iface_list=""
for iface in /sys/class/net/*; do
    name=$(basename $iface)
    if [ "$name" != "lo" ]; then
        iface_list="$iface_list $name"
    fi
done
for iface in $iface_list; do
    rx1=$(cat /sys/class/net/$iface/statistics/rx_bytes 2>/dev/null || echo 0)
    tx1=$(cat /sys/class/net/$iface/statistics/tx_bytes 2>/dev/null || echo 0)
    echo "$iface,$rx1,$tx1"
done
sleep 1
for iface in $iface_list; do
    rx2=$(cat /sys/class/net/$iface/statistics/rx_bytes 2>/dev/null || echo 0)
    tx2=$(cat /sys/class/net/$iface/statistics/tx_bytes 2>/dev/null || echo 0)
    echo "$iface,$rx2,$tx2"
done`
	out = runSSHCommand(client, netCmd)
	netLines := strings.Split(strings.TrimSpace(out), "\n")
	mid := len(netLines) / 2
	for i := 0; i < mid && i+mid < len(netLines); i++ {
		before := strings.Split(strings.TrimSpace(netLines[i]), ",")
		after := strings.Split(strings.TrimSpace(netLines[i+mid]), ",")
		if len(before) == 3 && len(after) == 3 && before[0] == after[0] {
			rx1, _ := strconv.ParseInt(before[1], 10, 64)
			tx1, _ := strconv.ParseInt(before[2], 10, 64)
			rx2, _ := strconv.ParseInt(after[1], 10, 64)
			tx2, _ := strconv.ParseInt(after[2], 10, 64)
			status.Net = append(status.Net, domain.SshNetInfo{
				Name:  before[0],
				RxBps: rx2 - rx1,
				TxBps: tx2 - tx1,
			})
		}
	}

	// Top processes
	out = runSSHCommand(client, "ps -eo pid,user,pcpu,pmem,comm --sort=-pcpu | head -n 11 | tail -n +2")
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) >= 5 {
			pid, _ := strconv.Atoi(f[0])
			cpu, _ := strconv.ParseFloat(f[2], 64)
			mem, _ := strconv.ParseFloat(f[3], 64)
			cmd := strings.Join(f[4:], " ")
			status.Procs = append(status.Procs, domain.SshProcInfo{
				Pid:     int32(pid),
				User:    f[1],
				Cpu:     cpu,
				Mem:     mem,
				Command: cmd,
			})
		}
	}

	return status
}

func runSSHCommand(client *ssh.Client, cmd string) string {
	session, err := client.NewSession()
	if err != nil {
		return ""
	}
	defer session.Close()
	out, err := session.CombinedOutput(cmd)
	if err != nil {
		return string(out)
	}
	return string(out)
}

func parseMemInfo(raw string) map[string]int64 {
	result := make(map[string]int64)
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			key := strings.TrimSuffix(fields[0], ":")
			val, _ := strconv.ParseInt(fields[1], 10, 64)
			result[key] = val * 1024 // convert KiB to bytes
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// Command snippet (favorites) handlers
// ---------------------------------------------------------------------------

func (a *Actor) handleCommandList(ctx actor.PureContext, _ domain.SshCommandListReq) (domain.SshCommandListResp, error) {
	if _, err := requireSSHCaller(ctx); err != nil {
		return domain.SshCommandListResp{}, err
	}
	items := make([]domain.SshCommandSnippet, len(a.Commands))
	copy(items, a.Commands)
	return domain.SshCommandListResp{Items: items}, nil
}

func (a *Actor) handleCommandCreate(ctx actor.PureContext, req domain.SshCommandCreateReq) (domain.SshCommandCreateResp, error) {
	if _, err := requireSSHCaller(ctx); err != nil {
		return domain.SshCommandCreateResp{}, err
	}
	if strings.TrimSpace(req.Name) == "" {
		return domain.SshCommandCreateResp{}, fmt.Errorf("command name is required")
	}
	snippet := domain.SshCommandSnippet{
		ID:       uuid.New().String(),
		Name:     req.Name,
		Content:  req.Content,
		Category: req.Category,
	}
	a.Commands = append(a.Commands, snippet)
	if err := a.Save(); err != nil {
		return domain.SshCommandCreateResp{}, fmt.Errorf("save failed: %w", err)
	}
	return domain.SshCommandCreateResp{Item: snippet}, nil
}

func (a *Actor) handleCommandUpdate(ctx actor.PureContext, req domain.SshCommandUpdateReq) (domain.SshCommandUpdateResp, error) {
	if _, err := requireSSHCaller(ctx); err != nil {
		return domain.SshCommandUpdateResp{}, err
	}
	for i := range a.Commands {
		if a.Commands[i].ID == req.ID {
			a.Commands[i].Name = req.Name
			a.Commands[i].Content = req.Content
			a.Commands[i].Category = req.Category
			if err := a.Save(); err != nil {
				return domain.SshCommandUpdateResp{}, fmt.Errorf("save failed: %w", err)
			}
			return domain.SshCommandUpdateResp{}, nil
		}
	}
	return domain.SshCommandUpdateResp{}, fmt.Errorf("command %q not found", req.ID)
}

func (a *Actor) handleCommandRemove(ctx actor.PureContext, req domain.SshCommandRemoveReq) (domain.SshCommandRemoveResp, error) {
	if _, err := requireSSHCaller(ctx); err != nil {
		return domain.SshCommandRemoveResp{}, err
	}
	for i, c := range a.Commands {
		if c.ID == req.ID {
			a.Commands = append(a.Commands[:i], a.Commands[i+1:]...)
			if err := a.Save(); err != nil {
				return domain.SshCommandRemoveResp{}, fmt.Errorf("save failed: %w", err)
			}
			return domain.SshCommandRemoveResp{}, nil
		}
	}
	return domain.SshCommandRemoveResp{}, fmt.Errorf("command %q not found", req.ID)
}

// ---------------------------------------------------------------------------
// Command history handlers
// ---------------------------------------------------------------------------

// maxHistorySize caps the persisted command history to avoid unbounded growth.
const maxHistorySize = 200

// sanitizeHistoryCmd strips ANSI escape sequences and other control characters
// from a raw PTY line, returning the trimmed human-readable command text.
func sanitizeHistoryCmd(line string) string {
	line = ansiEscapeSeq.ReplaceAllString(line, "")
	line = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1 // drop remaining control characters
		}
		return r
	}, line)
	return strings.TrimSpace(line)
}

// extractLines splits combined buffer+data on carriage-return or newline
// terminators (the line endings a PTY emits when the user presses Enter). It
// returns the complete lines plus the trailing partial fragment to carry over
// as the next buffer.
func extractLines(buf, data string) (lines []string, rest string) {
	combined := buf + data
	for {
		idx := strings.IndexAny(combined, "\r\n")
		if idx < 0 {
			return lines, combined
		}
		lines = append(lines, combined[:idx])
		combined = combined[idx+1:]
	}
}

// pushHistoryCmd inserts cmd at the front of history, removing any duplicate
// occurrences so only the most recent invocation survives, and trims the slice
// to at most max entries. Returns the input unchanged when cmd is empty.
func pushHistoryCmd(history []string, cmd string, max int) []string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return history
	}
	result := make([]string, 0, len(history)+1)
	result = append(result, cmd)
	for _, h := range history {
		if h != cmd {
			result = append(result, h)
		}
	}
	if len(result) > max {
		result = result[:max]
	}
	return result
}

// recordHistory buffers raw PTY input per session and, on each complete line,
// records the sanitized command into the actor's deduplicated history.
func (a *Actor) recordHistory(sess *session, data string) {
	sess.mu.Lock()
	lines, rest := extractLines(sess.lineBuf, data)
	sess.lineBuf = rest
	sess.mu.Unlock()

	recorded := false
	for _, line := range lines {
		cmd := sanitizeHistoryCmd(line)
		if cmd == "" {
			continue
		}
		a.History = pushHistoryCmd(a.History, cmd, maxHistorySize)
		recorded = true
	}
	if recorded {
		_ = a.Save()
	}
}

func (a *Actor) handleHistoryList(ctx actor.PureContext, req domain.SshHistoryListReq) (domain.SshHistoryListResp, error) {
	if _, err := requireSSHCaller(ctx); err != nil {
		return domain.SshHistoryListResp{}, err
	}
	limit := int(req.Limit)
	if limit <= 0 || limit > len(a.History) {
		limit = len(a.History)
	}
	items := make([]string, limit)
	copy(items, a.History[:limit])
	return domain.SshHistoryListResp{Items: items}, nil
}
