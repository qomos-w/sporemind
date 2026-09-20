package automesh

// This file consolidates package-level variable declarations for the automesh
// subpackage.

// --- Direction lookup ---

// 8-neighbor offsets in clockwise order (right-hand-rule), starting at E.
// Matches contours.d DIRECTIONS in nijigenerate.
var directions = [8][2]int{
	{+1, 0},  // E
	{+1, +1}, // SE
	{0, +1},  // S
	{-1, +1}, // SW
	{-1, 0},  // W
	{-1, -1}, // NW
	{0, -1},  // N
	{+1, -1}, // NE
}
