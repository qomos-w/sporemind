package filesystem

// This file consolidates package-level variable declarations for the
// filesystem package.

import "regexp"

// --- POSIX character-class handling ---

// posixCharClassRe matches POSIX character-class syntax [[:name:]] or [[:^name:]].
var posixCharClassRe = regexp.MustCompile(`\[\[:([\^]?)([a-z]+):\]\]`)

// posixToGoClass maps POSIX character-class names to Go regexp equivalents.
var posixToGoClass = map[string]string{
	"alnum":  `a-zA-Z0-9`,
	"alpha":  `a-zA-Z`,
	"ascii":  `\x00-\x7F`,
	"blank":  `\t `,
	"cntrl":  `\x00-\x1F\x7F`,
	"digit":  `0-9`,
	"graph":  `\x21-\x7E`,
	"lower":  `a-z`,
	"print":  `\x20-\x7E`,
	"punct":  `!"#$%&'()*+,\-./:;<=>?@\[\\\]^{|}~`,
	"space":  ` \t\n\r\f\v`,
	"upper":  `A-Z`,
	"word":   `a-zA-Z0-9_`,
	"xdigit": `0-9a-fA-F`,
}

// --- Walk configuration ---

// defaultSkipDirs are directories always skipped during recursive walks.
var defaultSkipDirs = map[string]bool{
	"node_modules": true,
	".git":         true,
	".svn":         true,
	"__pycache__":  true,
	".hg":          true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
}
