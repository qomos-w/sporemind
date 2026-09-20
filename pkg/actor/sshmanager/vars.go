package sshmanager

// This file consolidates package-level variable declarations for the sshmanager
// package.

import "regexp"

// --- ANSI escape handling ---

// ansiEscapeSeq matches CSI and OSC escape sequences emitted by terminal
// applications (arrow keys, color codes, etc.) that would pollute captured
// command text.
var ansiEscapeSeq = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\][^\x07]*\x07?`)
