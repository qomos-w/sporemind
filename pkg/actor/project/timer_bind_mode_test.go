package project

import (
	"strings"
	"testing"
)

func TestBindModeOf(t *testing.T) {
	cases := []struct {
		name string
		data string
		want string
	}{
		{name: "explicit bound wins", data: "  bind_mode: bound", want: schedulerBindModeBound},
		{name: "explicit ephemeral wins", data: "  bind_mode: ephemeral", want: schedulerBindModeEphemeral},
		{name: "legacy card defaults to bound", data: "", want: schedulerBindModeBound},
		{name: "legacy card with executor defaults to bound", data: "  executor: agent:coder", want: schedulerBindModeBound},
		{name: "invalid explicit value returned as-is", data: "  bind_mode: bogus", want: "bogus"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			card := schedulerCard("scheduler:bm", tc.data, "Body.")
			if got := bindModeOf(card); got != tc.want {
				t.Fatalf("bindModeOf(%q) = %q, want %q", tc.data, got, tc.want)
			}
		})
	}
}

func TestBoundAgentOf(t *testing.T) {
	cases := []struct {
		name string
		data string
		raw  string // frontmatter top-level fallback form (data block empty)
		want string
	}{
		{name: "data block wins", data: "  bound_agent: agent:coder", raw: "", want: "agent:coder"},
		{name: "frontmatter fallback", data: "", raw: "bound_agent: \"agent:legacy\"", want: "agent:legacy"},
		{name: "empty when unset", data: "", raw: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			card := schedulerCard("scheduler:ba", tc.data, "Body.")
			if tc.raw != "" {
				card.Raw = strings.Replace(card.Raw, "id: scheduler:ba\n", "id: scheduler:ba\n"+tc.raw+"\n", 1)
			}
			if got := boundAgentOf(card); got != tc.want {
				t.Fatalf("boundAgentOf = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLastRunAgentOf(t *testing.T) {
	cases := []struct {
		name string
		data string
		raw  string
		want string
	}{
		{name: "data block wins", data: "  last_run_agent: agent:sched-1", raw: "", want: "agent:sched-1"},
		{name: "frontmatter fallback", data: "", raw: `last_run_agent: "agent:old"`, want: "agent:old"},
		{name: "empty when unset", data: "", raw: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			card := schedulerCard("scheduler:lra", tc.data, "Body.")
			if tc.raw != "" {
				card.Raw = strings.Replace(card.Raw, "id: scheduler:lra\n", "id: scheduler:lra\n"+tc.raw+"\n", 1)
			}
			if got := lastRunAgentOf(card); got != tc.want {
				t.Fatalf("lastRunAgentOf = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBindModeWriteRoundTrip verifies the data: block write path used by the
// execution engine (setCardDataStringInRaw + store.Save) round-trips through
// decodeCard so the read-side helpers observe persisted values.
func TestBindModeWriteRoundTrip(t *testing.T) {
	card := schedulerCard("scheduler:rt", "  schedule_type: prompt", "Body.")
	card.Raw = setCardDataStringInRaw(card.Raw, "bind_mode", "ephemeral")
	card.Raw = setCardDataStringInRaw(card.Raw, "bound_agent", "agent:coder")
	card.Raw = setCardDataStringInRaw(card.Raw, "last_run_agent", "agent:sched-9")

	decoded := decodeCard(card.Title, card.Raw)
	if got := bindModeOf(decoded); got != schedulerBindModeEphemeral {
		t.Fatalf("bindModeOf after round-trip = %q, want %q", got, schedulerBindModeEphemeral)
	}
	if got := boundAgentOf(decoded); got != "agent:coder" {
		t.Fatalf("boundAgentOf after round-trip = %q, want %q", got, "agent:coder")
	}
	if got := lastRunAgentOf(decoded); got != "agent:sched-9" {
		t.Fatalf("lastRunAgentOf after round-trip = %q, want %q", got, "agent:sched-9")
	}
}