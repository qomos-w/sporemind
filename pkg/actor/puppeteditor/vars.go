package puppeteditor

// This file consolidates package-level variable declarations for the
// puppeteditor package.

// --- Node / param validation ---

// nodeKindWhitelist is the closed set of node Kind values the schema
// documents for PuppetNode. The explore query validates its Kind filter
// against it so an agent typo cannot silently match zero nodes.
var nodeKindWhitelist = map[string]bool{
	"part":      true,
	"composite": true,
	"mask":      true,
	"bone":      true,
	"group":     true,
	"node":      true,
}

// paramKindWhitelist is the closed set of parameter Kind values the schema
// documents for PuppetParam. The binding and validation code uses this set to
// reject unsupported parameter types without silently ignoring them.
var paramKindWhitelist = map[string]bool{
	"float": true,
	"int":   true,
	"bool":  true,
	"vec2":  true,
	"color": true,
}
