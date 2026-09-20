package llmclient

import "strings"

// Inline reasoning markers that some OpenAI-compatible providers (DeepSeek,
// GLM, Kimi) emit inside the regular content stream instead of the dedicated
// reasoning_content field. Built by concatenation so the literal marker tokens
// never appear verbatim in source.
var (
	thinkOpenTag  = "<" + "think" + ">"
	thinkCloseTag = "<" + "/think" + ">"
)

// markerPrefixLen returns the byte length of the longest suffix of s that is a
// proper prefix of marker. It lets a streaming filter hold back only the bytes
// that could still complete a tag after the next delta arrives.
func markerPrefixLen(s, marker string) int {
	limit := len(marker) - 1
	if len(s) < limit {
		limit = len(s)
	}
	for n := limit; n > 0; n-- {
		if strings.HasSuffix(s, marker[:n]) {
			return n
		}
	}
	return 0
}

// leadingThinkDivert separates inline think blocks from the regular content
// stream so they can be routed as reasoning. The filter is armed at the start
// of every text segment: at stream start, and again after each tool call ends
// (rearm), so content that follows a tool call in the same response is checked
// for a fresh leading think block. A think block that begins mid-text without
// a segment boundary passes through verbatim, so prose quoting the tag is
// never swallowed.
type leadingThinkDivert struct {
	state   int // tdIdle | tdInThink | tdDone
	pending string
}

const (
	tdIdle = iota
	tdInThink
	tdDone
)

// feed processes one text delta and returns the visible text and reasoning text
// to emit for it. Bytes that could be the start of a tag split across deltas are
// buffered until the next call resolves them.
func (f *leadingThinkDivert) feed(s string) (visible, reasoning string) {
	if f.state == tdDone {
		return s, ""
	}
	f.pending += s
	var vis, reas strings.Builder
	for f.state != tdDone && f.pending != "" {
		if f.state == tdInThink {
			if idx := strings.Index(f.pending, thinkCloseTag); idx >= 0 {
				reas.WriteString(f.pending[:idx])
				f.pending = f.pending[idx+len(thinkCloseTag):]
				f.state = tdDone
				vis.WriteString(f.pending) // text after the first block -> visible
				f.pending = ""
				continue
			}
			keep := markerPrefixLen(f.pending, thinkCloseTag)
			reas.WriteString(f.pending[:len(f.pending)-keep])
			f.pending = f.pending[len(f.pending)-keep:]
			break
		}
		// tdIdle: scanning for a leading opening tag.
		if idx := strings.Index(f.pending, thinkOpenTag); idx >= 0 {
			pre := f.pending[:idx]
			if strings.TrimSpace(pre) != "" {
				// Visible text appeared before any think tag: the block is not at
				// the start, so never divert. Pass everything through.
				vis.WriteString(f.pending)
				f.pending = ""
				f.state = tdDone
				break
			}
			vis.WriteString(pre) // leading whitespace, if any
			f.pending = f.pending[idx+len(thinkOpenTag):]
			f.state = tdInThink
			continue
		}
		keep := markerPrefixLen(f.pending, thinkOpenTag)
		content := f.pending[:len(f.pending)-keep]
		if strings.TrimSpace(content) == "" {
			break // only whitespace / a partial tag prefix; keep buffering
		}
		vis.WriteString(f.pending) // real text with no leading think block
		f.pending = ""
		f.state = tdDone
		break
	}
	return vis.String(), reas.String()
}

// rearm re-arms the filter at a segment boundary: a tool call ends the current
// text segment, so content deltas arriving after it are checked for a fresh
// leading think block again. No-op unless the filter is inert (tdDone); an
// open think block (tdInThink) is never disturbed. tdDone never holds buffered
// bytes, so dropping to tdIdle is safe.
func (f *leadingThinkDivert) rearm() {
	if f.state == tdDone {
		f.state = tdIdle
	}
}

// flush releases any bytes still buffered when the stream ends: reasoning for
// an unclosed first think block, otherwise ordinary visible text. After flush
// the filter is inert.
func (f *leadingThinkDivert) flush() (visible, reasoning string) {
	switch f.state {
	case tdInThink:
		reasoning = f.pending
	case tdIdle:
		visible = f.pending
	}
	f.pending = ""
	f.state = tdDone
	return
}
