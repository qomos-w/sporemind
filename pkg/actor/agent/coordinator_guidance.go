package agent

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

type coordinatorGuidanceState struct {
	Profiles map[string]domain.GuidanceCapabilityProfile `json:"profiles,omitempty"`
	Records  map[string][]domain.GuidanceRecord          `json:"records,omitempty"`
}

func (a *Actor) initCoordinatorGuidance() {
	if a.guidanceStore == nil {
		a.guidanceStore = persist.MustNew(config.PersistConfig("coordinator-guidance"))
	}
}

func guidanceAccount(ctx actor.PureContext, requested string) (string, error) {
	account := strings.TrimSpace(ctx.Identity().Subject)
	if account == "" || ctx.Identity().Role == "anonymous" {
		return "", errors.New("coordinator guidance requires an authenticated user")
	}
	if requested != "" && requested != account {
		return "", errors.New("coordinator guidance account does not match caller")
	}
	return account, nil
}

func (a *Actor) loadGuidance() (coordinatorGuidanceState, error) {
	a.initCoordinatorGuidance()
	state := coordinatorGuidanceState{Profiles: map[string]domain.GuidanceCapabilityProfile{}, Records: map[string][]domain.GuidanceRecord{}}
	err := a.guidanceStore.Load("profiles", &state)
	if errors.Is(err, persist.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if state.Profiles == nil {
		state.Profiles = map[string]domain.GuidanceCapabilityProfile{}
	}
	if state.Records == nil {
		state.Records = map[string][]domain.GuidanceRecord{}
	}
	return state, nil
}

func (a *Actor) saveGuidance(state coordinatorGuidanceState) error {
	return a.guidanceStore.Save("profiles", state)
}

func (a *Actor) handleCoordinatorGuidanceQuery(ctx actor.PureContext, req gen.GuidanceProfileQueryReq) (gen.GuidanceProfileQueryResp, error) {
	account, err := guidanceAccount(ctx, req.Account)
	if err != nil {
		return gen.GuidanceProfileQueryResp{}, err
	}
	a.guidanceMu.RLock()
	defer a.guidanceMu.RUnlock()
	state, err := a.loadGuidance()
	if err != nil {
		return gen.GuidanceProfileQueryResp{}, err
	}
	profile, ok := state.Profiles[account]
	resp := gen.GuidanceProfileQueryResp{Records: append([]domain.GuidanceRecord(nil), state.Records[account]...)}
	if ok {
		resp.Profile = &profile
	}
	return resp, nil
}

func (a *Actor) handleCoordinatorGuidanceUpdate(ctx actor.Context, req gen.GuidanceProfileUpdateReq) (gen.GuidanceProfileUpdateResp, error) {
	account, err := guidanceAccount(ctx, req.Account)
	if err != nil {
		return gen.GuidanceProfileUpdateResp{}, err
	}
	for _, entry := range req.Capabilities {
		if strings.TrimSpace(entry.Capability) == "" || entry.Familiarity < 0 || entry.Familiarity > 5 || entry.Confidence < 0 || entry.Confidence > 1 {
			return gen.GuidanceProfileUpdateResp{}, fmt.Errorf("invalid capability entry %q", entry.Capability)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	profile := domain.GuidanceCapabilityProfile{Account: account, Capabilities: req.Capabilities, UpdatedAt: now}
	for i := range profile.Capabilities {
		if profile.Capabilities[i].UpdatedAt == "" {
			profile.Capabilities[i].UpdatedAt = now
		}
	}
	a.guidanceMu.Lock()
	defer a.guidanceMu.Unlock()
	state, err := a.loadGuidance()
	if err != nil {
		return gen.GuidanceProfileUpdateResp{}, err
	}
	state.Profiles[account] = profile
	if req.Records != nil {
		state.Records[account] = append([]domain.GuidanceRecord(nil), req.Records...)
	}
	if err := a.saveGuidance(state); err != nil {
		return gen.GuidanceProfileUpdateResp{}, err
	}
	return gen.GuidanceProfileUpdateResp{Profile: profile}, nil
}

func (a *Actor) handleCoordinatorGuidanceClear(ctx actor.Context, req gen.GuidanceProfileClearReq) (gen.GuidanceProfileClearResp, error) {
	account, err := guidanceAccount(ctx, req.Account)
	if err != nil {
		return gen.GuidanceProfileClearResp{}, err
	}
	a.guidanceMu.Lock()
	defer a.guidanceMu.Unlock()
	state, err := a.loadGuidance()
	if err != nil {
		return gen.GuidanceProfileClearResp{}, err
	}
	profile, ok := state.Profiles[account]
	cleared := false
	if req.Capability == "" {
		cleared = ok
		delete(state.Profiles, account)
		delete(state.Records, account)
	} else if ok {
		remaining := profile.Capabilities[:0]
		for _, entry := range profile.Capabilities {
			if entry.Capability == req.Capability {
				cleared = true
			} else {
				remaining = append(remaining, entry)
			}
		}
		profile.Capabilities = remaining
		state.Profiles[account] = profile
	}
	if cleared {
		if err := a.saveGuidance(state); err != nil {
			return gen.GuidanceProfileClearResp{}, err
		}
	}
	return gen.GuidanceProfileClearResp{Account: account, Cleared: cleared}, nil
}

func coordinatorGuidancePrompt(profile *domain.GuidanceCapabilityProfile) string {
	if profile == nil || len(profile.Capabilities) == 0 {
		return ""
	}
	entries := append([]domain.GuidanceCapabilityEntry(nil), profile.Capabilities...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Capability < entries[j].Capability })
	var b strings.Builder
	b.WriteString("<user_guidance_profile>\n")
	for _, entry := range entries {
		fmt.Fprintf(&b, "%s: familiarity=%d confidence=%.2f\n", entry.Capability, entry.Familiarity, entry.Confidence)
	}
	b.WriteString("Use this profile to tailor explanations; treat low confidence as uncertain and never reveal internal scores.\n</user_guidance_profile>")
	return b.String()
}

func coordinatorGuidanceDecision(text string, profile *domain.GuidanceCapabilityProfile) string {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return ""
	}
	for _, marker := range []string{"how do i", "where is", "what is", "i don't understand", "i am confused", "怎么", "哪里", "不懂", "疑惑"} {
		if strings.Contains(lower, marker) {
			return "help"
		}
	}
	if profile == nil {
		return "onboarding"
	}
	return "answer"
}

const (
	guidanceConfidenceUserDeclared = 0.8
	guidanceBehavioralConfidence   = 0.3
	guidanceMaxFamiliarity         = 5
)

// Proactive hint capability keys used by the coordinator. Each key is scoped
// to an account and persisted as a GuidanceCapabilityEntry with HintState.
const (
	coordinatorHintFirstToolCall = "hint.first-tool-call"
)

// Proactive hint state machine. Empty string is treated as idle.
const (
	guidanceHintStateIdle      = "idle"
	guidanceHintStateTriggered = "triggered"
	guidanceHintStateDismissed = "dismissed"
)

// hintState returns the canonical state of a guidance hint capability.
// Unknown or empty values normalize to idle so the guard cannot get stuck.
func hintState(entry *domain.GuidanceCapabilityEntry) string {
	if entry == nil {
		return guidanceHintStateIdle
	}
	switch entry.HintState {
	case guidanceHintStateTriggered, guidanceHintStateDismissed:
		return entry.HintState
	default:
		return guidanceHintStateIdle
	}
}

// claimCoordinatorHint checks whether the named hint capability is idle for
// the authenticated caller. If so, it atomically sets HintState to triggered
// and persists the updated profile. It returns true exactly once per account
// (global one-shot) and false on every subsequent call. Errors are returned
// only when persistence or identity resolution fails.
func (a *Actor) claimCoordinatorHint(ctx actor.Context, capability string) (bool, error) {
	account, err := guidanceAccount(ctx, "")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(capability) == "" {
		return false, fmt.Errorf("coordinator guidance hint: capability is required")
	}
	a.guidanceMu.Lock()
	defer a.guidanceMu.Unlock()
	state, err := a.loadGuidance()
	if err != nil {
		return false, err
	}
	profile := state.Profiles[account]
	now := time.Now().UTC().Format(time.RFC3339)
	var existing *domain.GuidanceCapabilityEntry
	for i := range profile.Capabilities {
		if profile.Capabilities[i].Capability == capability {
			existing = &profile.Capabilities[i]
			break
		}
	}
	if hintState(existing) != guidanceHintStateIdle {
		return false, nil
	}
	updated := domain.GuidanceCapabilityEntry{
		Capability:  capability,
		Familiarity: 0,
		Confidence:  guidanceBehavioralConfidence,
		HintState:   guidanceHintStateTriggered,
		UpdatedAt:   now,
	}
	if existing != nil {
		updated.Familiarity = existing.Familiarity
		if len(existing.Evidence) > 0 {
			updated.Evidence = append([]string(nil), existing.Evidence...)
		}
		*existing = updated
	} else {
		profile.Capabilities = append(profile.Capabilities, updated)
	}
	profile.Account = account
	if profile.UpdatedAt == "" {
		profile.UpdatedAt = now
	}
	state.Profiles[account] = profile
	if err := a.saveGuidance(state); err != nil {
		return false, err
	}
	return true, nil
}

// applyUsageSignal is a pure function that merges a behavioral usage signal
// into a capability entry. It never modifies entries the user has explicitly
// declared (confidence >= guidanceConfidenceUserDeclared); otherwise it
// increments familiarity by one (capped at guidanceMaxFamiliarity), tags the
// result with low behavioral confidence, and appends evidence.
func applyUsageSignal(existing *domain.GuidanceCapabilityEntry, signal string, evidence string, now string) domain.GuidanceCapabilityEntry {
	result := domain.GuidanceCapabilityEntry{
		Familiarity: 0,
		Confidence:  guidanceBehavioralConfidence,
		UpdatedAt:   now,
	}
	if existing != nil {
		result.Capability = existing.Capability
		if existing.Confidence >= guidanceConfidenceUserDeclared {
			return *existing
		}
		result.Familiarity = existing.Familiarity
		result.Evidence = append([]string(nil), existing.Evidence...)
	}
	if result.Familiarity < guidanceMaxFamiliarity {
		result.Familiarity++
	}
	result.Confidence = guidanceBehavioralConfidence
	if signal != "" {
		result.Evidence = append(result.Evidence, signal+":"+now)
	}
	if evidence != "" {
		result.Evidence = append(result.Evidence, evidence)
	}
	return result
}

func (a *Actor) handleCoordinatorGuidanceIncrement(ctx actor.Context, req gen.GuidanceProfileIncrementReq) (gen.GuidanceProfileIncrementResp, error) {
	account, err := guidanceAccount(ctx, req.Account)
	if err != nil {
		return gen.GuidanceProfileIncrementResp{}, err
	}
	if strings.TrimSpace(req.Capability) == "" {
		return gen.GuidanceProfileIncrementResp{}, fmt.Errorf("coordinator guidance increment: capability is required")
	}
	if req.Signal == "" {
		return gen.GuidanceProfileIncrementResp{}, fmt.Errorf("coordinator guidance increment: signal is required")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	a.guidanceMu.Lock()
	defer a.guidanceMu.Unlock()
	state, err := a.loadGuidance()
	if err != nil {
		return gen.GuidanceProfileIncrementResp{}, err
	}
	profile := state.Profiles[account]
	var existing *domain.GuidanceCapabilityEntry
	for i := range profile.Capabilities {
		if profile.Capabilities[i].Capability == req.Capability {
			existing = &profile.Capabilities[i]
			break
		}
	}
	updated := applyUsageSignal(existing, req.Signal, req.Evidence, now)
	if existing != nil {
		*existing = updated
	} else {
		updated.Capability = req.Capability
		profile.Capabilities = append(profile.Capabilities, updated)
	}
	profile.Account = account
	profile.UpdatedAt = now
	state.Profiles[account] = profile
	if err := a.saveGuidance(state); err != nil {
		return gen.GuidanceProfileIncrementResp{}, err
	}
	return gen.GuidanceProfileIncrementResp{Entry: updated}, nil
}
