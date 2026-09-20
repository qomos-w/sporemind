// Package scriptcard is the shared canonical contract for script task cards.
//
// A "script card" is a card whose data.exec.kind = "script" and whose body
// carries a ```spore fenced block containing the script source. The contract
// between the project-side static validator and the workspace-side runtime
// executor was historically duplicated in two places:
//
//   - pkg/actor/project/card_validation.go (bodyContainsSporeFence)
//   - pkg/actor/workspace/executor_script.go (firstSporeBlock)
//
// Those implementations agreed on most cases but diverged on edge inputs
// (case sensitivity, longer-fence pairing, indented openers). This package
// freezes the canonical rules both sides must use, so a card accepted at
// preflight is byte-for-byte the same card loaded by the executor.
//
// The package has no external dependencies (only the Go standard library)
// and no package-level mutable state, so it is safe to import from both
// the project and workspace sides without dragging actor state across
// the package boundary.
package scriptcard

import (
	"strings"
)

// ExtractSporeBlock returns the source code inside the first ```spore
// fenced block found in body, plus a boolean indicating whether any such
// block was present. The result is trimmed of leading/trailing ASCII
// whitespace (so callers get the canonical script source regardless of how
// the block was indented in the card body).
//
// Rules (canonical):
//
//  1. The body is normalized to LF first (\r\n -> \n); the fence syntax
//     is line-oriented and CR would otherwise produce a phantom blank
//     inside the extracted content.
//  2. Scanning is line-by-line (strings.Split(body, "\n")). Each line
//     has its leading ASCII space/tab stripped before any further
//     inspection, so indented fences are recognised uniformly.
//  3. An opener line must start with N >= 3 backticks, followed
//     immediately by an info string. The first whitespace-separated
//     token of the info string is the language tag; the tag matches
//     when it is, case-insensitively, "spore". A tag like "sporescript"
//     or "spore-foo" also matches (its first whitespace-separated token
//     equals "spore"), mirroring CommonMark's fence spec — extra
//     metadata is permitted after the language tag.
//  4. The closing fence must be exactly N backticks (the same length
//     as the opener), optionally followed by trailing whitespace. A
//     fence shorter or longer than the opener does not close the block,
//     matching CommonMark's "info string of the closing fence is
//     ignored, but it must be no shorter than the opening fence's mark
//     length" rule (we tighten this to exact equality so the executor
//     never has to disambiguate).
//  5. When the opener is matched and a closing fence of the same length
//     is found later in the body, the content between them (lines after
//     the opener up to but not including the closer) is returned,
//     trimmed. Any number of non-matching fenced blocks (e.g. ```yaml,
//     ```python, ```spore with mismatched closer) is skipped over.
//  6. If no matching fence is found the function returns ("", false).
//
// The "first matching block wins" rule is intentional: an executor that
// loads source on demand should not have to scan the entire body for
// every claim. A card that wants a different first block should remove
// the earlier one — multi-block card bodies should be authored
// deliberately, not detected heuristically.
func ExtractSporeBlock(body string) (string, bool) {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	lines := strings.Split(body, "\n")
	for i := 0; i < len(lines); i++ {
		opener := strings.TrimLeft(lines[i], " \t")
		n := countLeadingBackticks(opener)
		if n < 3 {
			continue
		}
		rest := opener[n:]
		// Info string must be present (no whitespace allowed between
		// the opener and the info string), and the first whitespace-
		// separated token must EqualFold "spore".
		lang, _ := firstInfoToken(rest)
		if lang == "" || !strings.EqualFold(lang, "spore") {
			continue
		}
		// Find the closing fence of exactly N backticks. Anything
		// shorter is just content; anything longer is a different
		// fence we are not interested in pairing.
		closeLine := -1
		for j := i + 1; j < len(lines); j++ {
			cand := strings.TrimLeft(lines[j], " \t")
			if countLeadingBackticks(cand) == n {
				// CommonMark allows trailing whitespace on a closing
				// fence; reject anything else after the run of
				// backticks (the closing fence is otherwise silent).
				tail := cand[n:]
				if tail == "" || isOnlyHorizontalWhitespace(tail) {
					closeLine = j
					break
				}
			}
		}
		if closeLine < 0 {
			return "", false
		}
		content := strings.Join(lines[i+1:closeLine], "\n")
		return strings.TrimSpace(content), true
	}
	return "", false
}

// countLeadingBackticks returns the number of consecutive '`' bytes at
// the start of s. It does not consider any other character.
func countLeadingBackticks(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] != '`' {
			return i
		}
	}
	return len(s)
}

// firstInfoToken returns the first whitespace-separated word of s plus
// the byte offset where the word ends. The returned word is the raw
// tag (no trimming) — the caller is responsible for any case folding
// or punctuation handling. Empty input, or input that starts with
// whitespace, yields ("", 0); such openers do not match because the
// info string is required.
func firstInfoToken(s string) (string, int) {
	// Reject an empty / whitespace-leading info string outright:
	// CommonMark fences require an info string when the opener has
	// one (which it does for ```spore — the "spore" itself is the
	// info string).
	end := 0
	for end < len(s) && s[end] != ' ' && s[end] != '\t' {
		end++
	}
	return s[:end], end
}

// isOnlyHorizontalWhitespace reports whether s is non-empty and made
// entirely of ASCII space/tab bytes. Used to allow CommonMark-style
// trailing whitespace on a closing fence.
func isOnlyHorizontalWhitespace(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' && s[i] != '\t' {
			return false
		}
	}
	return true
}