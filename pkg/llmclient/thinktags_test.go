package llmclient

import "testing"

func TestLeadingThinkDivert(t *testing.T) {
	// Each case feeds a sequence of deltas, then flushes, accumulating the
	// visible and reasoning text emitted across feed() and flush().
	cases := []struct {
		name string
		deltas []string
		wantVis string
		wantReason string
	}{
		{
			"leading_block_diverted",
			[]string{thinkOpenTag + "hmm" + thinkCloseTag + "answer"},
			"answer", "hmm",
		},
		{
			// open tag split across two deltas
			"split_open_tag",
			[]string{"<", thinkOpenTag[1:] + "hmm", thinkCloseTag + "answer"},
			"answer", "hmm",
		},
		{
			// visible text before any tag => never divert, tags stay visible
			"text_before_tag_no_divert",
			[]string{"hello " + thinkOpenTag + "x" + thinkCloseTag + " world"},
			"hello " + thinkOpenTag + "x" + thinkCloseTag + " world", "",
		},
		{"no_think_block", []string{"just answer"}, "just answer", ""},
		{
			// only the first block diverts; a second block passes through verbatim
			"only_first_block_diverted",
			[]string{thinkOpenTag + "a" + thinkCloseTag + "txt " + thinkOpenTag + "b" + thinkCloseTag},
			"txt " + thinkOpenTag + "b" + thinkCloseTag, "a",
		},
		{
			// leading whitespace buffers until real text or a tag resolves it
			"whitespace_then_text",
			[]string{"\n", "answer"},
			"\nanswer", "",
		},
		{
			// unclosed first block: reasoning streams eagerly; flush releases
			// a buffered partial close-tag prefix as reasoning
			"unclosed_partial_close",
			[]string{thinkOpenTag + "abc" + thinkCloseTag[:2]},
			"", "abc" + thinkCloseTag[:2],
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var f leadingThinkDivert
			var vis, reas string
			for _, d := range c.deltas {
				v, r := f.feed(d)
				vis += v
				reas += r
			}
			v, r := f.flush()
			vis += v
			reas += r
			if vis != c.wantVis {
				t.Errorf("visible = %q, want %q", vis, c.wantVis)
			}
			if reas != c.wantReason {
				t.Errorf("reasoning = %q, want %q", reas, c.wantReason)
			}
		})
	}
}

// TestLeadingThinkDivertInertAfterDone verifies that once the filter is done
// (first block consumed or no diversion), subsequent feeds pass through verbatim.
func TestLeadingThinkDivertInertAfterDone(t *testing.T) {
	var f leadingThinkDivert
	_, _ = f.feed("plain text") // real text => tdDone, no diversion
	if got, _ := f.feed(thinkOpenTag + "late" + thinkCloseTag); got != thinkOpenTag+"late"+thinkCloseTag {
		t.Errorf("after done, feed = %q, want verbatim passthrough", got)
	}
}

// TestLeadingThinkDivertRearmAfterToolCall verifies the segment-boundary
// semantics: a tool call ends the text segment, so a think block in content
// after it diverts again, while the same block without a rearm passes through.
func TestLeadingThinkDivertRearmAfterToolCall(t *testing.T) {
	var f leadingThinkDivert
	var vis, reas string
	add := func(v, r string) { vis += v; reas += r }
	v, r := f.feed("before tool ")
	add(v, r)
	f.rearm()
	v, r = f.feed(thinkOpenTag + "second" + thinkCloseTag + "after tool")
	add(v, r)
	v, r = f.flush()
	add(v, r)
	if vis != "before tool after tool" {
		t.Errorf("visible = %q, want %q", vis, "before tool after tool")
	}
	if reas != "second" {
		t.Errorf("reasoning = %q, want %q", reas, "second")
	}

	// Without rearm the same trailing block stays visible.
	var g leadingThinkDivert
	vis, _ = g.feed("plain " + thinkOpenTag + "x" + thinkCloseTag)
	if vis != "plain "+thinkOpenTag+"x"+thinkCloseTag {
		t.Errorf("no rearm: visible = %q, want verbatim", vis)
	}
}
