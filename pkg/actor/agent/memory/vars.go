package memory

// This file consolidates package-level variable declarations for the memory
// subpackage.

import "strings"

// --- Filename sanitization ---

// sanitizeFileName replaces characters that are invalid in Windows filenames
// (e.g. ':' in node IDs like "role:coder") with underscores. The original
// node ID is preserved in the monocard frontmatter and the manifest.
//
// TODO(actor-ownership): migrate to actor-owned state
var fileNameSanitizer = strings.NewReplacer(
	`:`, "_",
	`<`, "_",
	`>`, "_",
	`"`, "_",
	`\`, "_",
	`|`, "_",
	`?`, "_",
	`*`, "_",
)
