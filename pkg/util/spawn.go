package util

import (
	"context"
	"os/exec"
)

// Command constructs an exec.Cmd with HideWindow applied. Every child-process
// spawn in the backend must go through this constructor (or CommandContext) so
// console windows can never flash on Windows; on other platforms it is
// equivalent to exec.Command. Enforced by spawn_guard_test.go.
func Command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	HideWindow(cmd)
	return cmd
}

// CommandContext is the context-aware variant of Command.
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	HideWindow(cmd)
	return cmd
}
