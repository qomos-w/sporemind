package shell

// This file consolidates package-level variable declarations for the shell
// package.

// --- Command safety ---

// DangerousCmds blocks commands that could cause irreversible damage or data exfiltration.
// curl is intentionally not listed — local debug endpoints (read-logs, debug-desktop skills) rely on it.
var DangerousCmds = map[string]bool{
	"wget": true, "nc": true, "netcat": true,
	"ssh": true, "scp": true, "sftp": true, "telnet": true,
	"sudo": true, "su": true, "doas": true,
	"mkfs": true, "dd": true, "fdisk": true,
}

// --- Built-in VFS commands ---

// builtins maps command names to native Go implementations.
var builtins = map[string]func(vfs VFS, args []string) (stdout, stderr string, exitCode int){
	"ls":    cmdLs,
	"cat":   cmdCat,
	"mkdir": cmdMkdir,
	"rm":    cmdRm,
	"cp":    cmdCp,
	"mv":    cmdMv,
	"pwd":   cmdPwd,
	"echo":  cmdEcho,
	"write": cmdWrite,
}
