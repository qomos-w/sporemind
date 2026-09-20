---
id: builtin:bundle:ssh-tools
type: bundle
title: SSH Tools
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: server
  visual:
    icon: server
    accent: indigo
    color: "#4f46e5"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  protected: true
  settingsVisible: true
  tools:
    - sshmanager.host_list
    - sshmanager.status_get
    - sshmanager.status_list
    - sshmanager.session_list
    - sshmanager.shell_open
    - sshmanager.shell_run
    - sshmanager.download
    - sshmanager.upload
---

## SSH Tool Usage

Operate on remote SSH hosts saved in the SSH manager. The bundle exposes eight callables: `sshmanager.host_list` to discover saved hosts, `sshmanager.status_get` / `sshmanager.status_list` to collect read-only runtime status (CPU, memory, load, disk, network, top processes, uptime) from one or all hosts, `sshmanager.session_list` to list all currently open interactive shell sessions, `sshmanager.shell_open` to open a new interactive PTY session on a remote host, `sshmanager.shell_run` to execute a command on an existing interactive shell session (preserving cwd/env/venv state across calls), `sshmanager.download` to download a remote file or directory (auto-detects file vs directory; directories are archived as tar.gz), and `sshmanager.upload` to upload content to a remote path (raw file or tar.gz archive extract).

> **No invisible remote commands** — `sshmanager.exec` is intentionally excluded from this bundle. Every remote command must run through an interactive shell session (`shell_open` + `shell_run`) so the human can see the command in the frontend terminal before and while it executes. Never attempt to bypass this by calling `sshmanager.exec` directly; it is not exposed to the agent.

> **Boundary with shell_exec**: use SSH tools only when the command must run on a **remote** host configured in the SSH manager. For anything on the local project machine, use `project.shell_exec` instead. Never route local work through SSH.

### sshmanager.host_list

List the SSH hosts saved in the SSH manager.

- **Parameters**: none.
- **Returns**: `items` — array of host views. Each item has `Id`, `Name`, `Group`, and `HasCredential` (whether a usable credential is stored). Connection details (`Host` address, `Port`, `User`, `AuthMethod`) are hidden from agent callers — identify hosts by `Name`/`Id` only; never ask the user for or echo an IP.

Call this first to obtain the `hostId` needed by `sshmanager.shell_open` or `sshmanager.status_get`. A host with `HasCredential=false` cannot be used until credentials are configured (outside the tool loop).

Hosts marked **Agent invisible** in the SSH settings are excluded from this list and from every other SSH tool — sessions, status, shell — as if they did not exist. That is a deliberate owner decision; never ask the user to unhide a host or work around it.

### sshmanager.status_get

Collect read-only runtime status from a single remote SSH host.

- **Parameters**:
  - `hostId` — ID of the saved SSH host to inspect (from `sshmanager.host_list`).
- **Returns**: a `Status` object with `HostName`, `Connected`, `CpuPercent`, `MemUsed`/`MemTotal`, `SwapUsed`/`SwapTotal`, `LoadAvg` (1/5/15-min), `Disks` (mount point, total, available), `Net` (per-interface Rx/Tx bytes per second), `Procs` (top processes by CPU), `Uptime` (seconds), and `Timestamp`. A host that fails to connect returns `Connected=false` with no error.

The actor dials the host directly and runs a fixed set of read-only inspection commands (`cat /proc/*`, `df`, `ps`, `top`, `/sys/class/net`); it does not execute caller-supplied commands. Prefer this over `sshmanager.status_list` when you only need one host.

### sshmanager.status_list

Collect the same read-only runtime status as `sshmanager.status_get` from every saved SSH host.

- **Parameters**: none.
- **Returns**: `Items` — array of `Status` objects, one per saved host, in declaration order. Hosts that fail to connect are included with `Connected=false`.

This dials each host in turn (and samples network bandwidth with a 1-second interval), so it is slower than `status_get`. Use it for a fleet overview, and `status_get` for a single host.

### sshmanager.shell_open

Open a new interactive PTY session on a remote host. Returns a session ID for use with `sshmanager.shell_run`.

> **Check first** — Before calling `shell_open`, call `sshmanager.session_list` to check whether a session for the target host already exists. Reuse that `sessionId` with `sshmanager.shell_run` instead of opening a duplicate. Each `shell_open` creates a separate PTY; duplicating sessions wastes resources and hides commands from the human's terminal view.

- **Parameters**:
  - `hostId` — ID of the saved SSH host (from `sshmanager.host_list`).
  - `initialCols` — Optional terminal width in columns (default 80).
  - `initialRows` — Optional terminal height in rows (default 24).
- **Returns**: `SessionID`, `Connected` (true on success), `Error` (empty on success).

The session stays open until closed via `sshmanager.shell_close` or the SSH connection drops. Once open, use `sshmanager.shell_run` to execute commands in the session with persistent cwd/env/venv state.

### sshmanager.shell_run

Run a command on an existing interactive SSH shell session and return the text output with exit code. Environment state (cwd, env vars, venv) is preserved across calls within the same session.

- **Parameters**:
  - `sessionId` — ID of the open shell session (from `sshmanager.shell_open`).
  - `command` — Shell command to execute in the session.
  - `timeoutMs` — Optional timeout in milliseconds. Default 30000 (30s). Values below 1000 are clamped to the default; larger values are allowed.
- **Returns**: `Output` (plain-text PTY output for this command, ANSI stripped, echoed command line removed), `ExitCode` (from the sentinel marker; -1 if timed out or not found), `Truncated` (true if output exceeded the byte budget), and `TimedOut` (true if the timeout expired before the command completed).

The session must already exist — call `sshmanager.session_list` first to find an existing session for the target host; if none exists, open one with `sshmanager.shell_open` (or `open_ssh_session`). Reuse existing sessions across multiple `shell_run` calls; do not open duplicates. `shell_run` writes the command to the session's stdin, appends a sentinel marker to detect completion and capture the exit code, drains the PTY output until the sentinel arrives or the timeout expires, then returns the plain-text result. `shell_run` reuses the persistent interactive session so `cd`, `export`, and `source venv/bin/activate` carry over between calls. Not suitable for non-terminating commands (`tail -f`, `top`) — these never produce a sentinel and will time out.

### sshmanager.session_list

List all currently open interactive SSH shell sessions. **Call this first** before `shell_open` or `shell_run` to find an existing session for the target host.

- **Parameters**: none.
- **Returns**: `Items` — array of `SshSessionInfo` with `SessionID`, `HostID`, `HostName`, `Connected`, and `Cwd`. (`HostAddr` and `User` are hidden from agent callers; match sessions by `HostId`/`HostName`.)

All open sessions are returned regardless of who opened them (human frontend or agent). Call this **before** opening a new session — if the user already has a terminal open on the target host, reuse that `sessionId` with `shell_run` so the user can see the commands in their terminal.

### sshmanager.download

Download a remote file or directory from an open SSH session. Returns the content as base64.

- **Parameters**:
  - `sessionId` — ID of an open shell session (from `sshmanager.session_list` or `sshmanager.shell_open`).
  - `path` — Remote file or directory path to download.
- **Returns**: `Name` (basename of the downloaded path), `Content` (base64-encoded bytes — raw file bytes for a file, tar.gz archive for a directory), `Size` (decoded payload size in bytes), `IsDirectory` (true if the path was a directory; Content is a tar.gz), `NumEntries` (archived entry count, only when IsDirectory is true).

The callable auto-detects whether the path is a regular file or a directory. For a file, it reads the bytes and base64-encodes them (capped at 50 MB). For a directory, it recursively archives the tree into a tar.gz and base64-encodes the result (capped at the compressed archive size limit). Use the `IsDirectory` flag to determine how to interpret `Content` — a directory download is a tar.gz that must be extracted before use.

> **Requires an open session** — `download` operates on a connected SSH session. Call `sshmanager.session_list` first to find an existing session for the target host; open one with `sshmanager.shell_open` if none exists.

### sshmanager.upload

Upload base64-encoded content to a remote path on an open SSH session. Supports both raw file writes and tar.gz archive extraction.

- **Parameters**:
  - `sessionId` — ID of an open shell session (from `sshmanager.session_list` or `sshmanager.shell_open`).
  - `path` — Remote target path (a file path for raw upload, a directory path for archive extraction).
  - `content` — Base64-encoded file bytes (when `isArchive` is false) or tar.gz archive bytes (when `isArchive` is true).
  - `isArchive` — `true` to extract a tar.gz archive into the target directory; `false` to write raw bytes to the target file.
- **Returns**: `NumEntries` (entries extracted, only when `isArchive` is true), `BytesWritten` (total bytes written to the remote filesystem).

When `isArchive` is true, the content is decoded as a tar.gz and safely extracted into the target directory — the directory is auto-created if it does not exist. When `isArchive` is false, the content is decoded as raw bytes and written to the target file path, with parent directories auto-created. Symlink and traversal entries in tar.gz archives are rejected by the extraction validation.

> **Approval required** — `upload` is registered as an irreversible effect; it is subject to the standard approval chain before execution.
> **Requires an open session** — `upload` operates on a connected SSH session. Call `sshmanager.session_list` first to find an existing session for the target host; open one with `sshmanager.shell_open` if none exists.

### Rules

1. **Remote only** — Use SSH tools for operations that must run on a remote host. For local project commands (build, test, git), use `project.shell_exec`; do not tunnel local work through SSH.
2. **Discover first** — Always call `sshmanager.host_list` to resolve the correct `hostId` before `sshmanager.shell_open` or `sshmanager.status_get`. Do not guess host IDs.
3. **Reuse existing sessions** — Before opening a new session, call `sshmanager.session_list`. If a session for the target host is already open (typically opened by the human in the frontend terminal), reuse its `sessionId` with `sshmanager.shell_run` rather than opening a duplicate. The human can watch commands execute in real time only if you use their session.
4. **Open only when no session exists** — Call `sshmanager.shell_open` only when `session_list` shows no open session for the target host. Each `shell_open` creates a separate PTY; duplicating sessions wastes resources and hides commands from the human's terminal view.
5. **Prefer dedicated tools for file ops** — For remote file read/write/list/archive, prefer the dedicated `sshmanager.file_*` / `sshmanager.archive_*` callables over shell commands when available. For composite upload/download of files or directories, use `sshmanager.upload` / `sshmanager.download`.
6. **Set an adequate timeout** — Long-running remote commands (builds, large transfers) need a generous timeout; a timeout produces a `timed out` output rather than a hung call.
7. **Check exit code** — Always inspect `ExitCode` from `shell_run`. Non-zero means the command failed; read `Output` for the reason.
8. **Prefer `status_get` over `status_list`** — `status_list` dials every saved host and samples network bandwidth with a 1-second interval, so it is much slower. Use it only for a fleet overview; inspect a single host with `status_get`.
9. **Not for non-terminating commands** — `shell_run` is not suitable for non-terminating commands (`tail -f`, `top`) — these never produce a sentinel and will time out.

### Security tips

1. **Remote effect, real consequences** — `shell_run` runs on a production-grade remote machine under that host's user account. Treat every command as irreversible and destructive until proven otherwise.
2. **Prefer read-only first** — Inspect state (`ls`, `cat`, `status`) before issuing mutating commands (`rm`, `mv`, `systemctl`, package installs).
3. **No secrets in output** — Command output may contain credentials, tokens, or keys. Do not echo credentials into the response unless the user explicitly needs them.
4. **Approval required** — `sshmanager.shell_run` and `sshmanager.upload` are registered as irreversible effects; they are subject to the standard approval chain before execution.
