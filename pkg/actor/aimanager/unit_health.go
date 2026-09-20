package aimanager

import (
	"sort"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/llmclient"
)

// disableWindowProvider is a configured provider currently inside one of its
// daily disable windows: deadline is when the union of active windows ends and
// models enumerates the units the operator policy blocks.
type disableWindowProvider struct {
	deadline int64
	models   []domain.ProviderModel
}

// activeDisableWindows computes the per-provider disable-window projection from
// the configured providers. It mirrors handleProviderList: DisableUntil is an
// operator policy that must hard-block the unit, so it surfaces in the global
// health snapshot the same way failure-driven cooldown does.
func (a *Actor) activeDisableWindows(now time.Time) map[string]disableWindowProvider {
	a.mu.RLock()
	defer a.mu.RUnlock()
	windows := make(map[string]disableWindowProvider, len(a.Providers))
	for _, p := range a.Providers {
		if dd := providerDisableDeadline(p.DisableWindows, now); dd > 0 {
			windows[p.Name] = disableWindowProvider{deadline: dd, models: p.Models}
		}
	}
	return windows
}

// handleUnitHealthList projects the llmclient provider-level health registry
// into a global cooldown snapshot that the composer can consume directly,
// independent of any aggregator tree. Only non-healthy units (active cooldown
// or disabled) are included; expired cooldowns are normalized to healthy and
// omitted. DispatchActivity is left empty — it is aggregator-local.
//
// Active provider disable windows are overlaid with precedence over failure
// cooldown (the DisableUntil semantics declared in the schema): the entry
// projects cooling_down + disable_window and its CooldownUntil is the later of
// the two deadlines, so the composer countdown cannot expire while the window
// still blocks the unit. llmclient "disabled" state wins over the window —
// auth/quota failures need manual recovery, not time-based expiry. Units under
// an active window but absent from the failure registry (healthy in llmclient)
// are synthesized from the provider's model list; without them the composer
// would render those rows as healthy while the aggregator hard-rejects them.
//
// This callable is the shared global cooldown source. The aggregator status
// (AIAggregatorStatusResp) no longer serves cooldown to the composer; it
// retains only structural pool info and in-flight dispatch activity.
func (a *Actor) handleUnitHealthList(_ actor.PureContext) (domain.AIManagerUnitHealthListResp, error) {
	snap := llmclient.HealthSnapshot()
	now := time.Now()
	windows := a.activeDisableWindows(now)

	items := make([]domain.AICallableUnitView, 0, len(snap)+len(windows))
	surfaced := make(map[string]bool, len(snap))
	for k, h := range snap {
		// Skip healthy units — the snapshot only contains units that have
		// been recorded (had failures); healthy units not in the registry
		// are implicitly healthy and not surfaced.
		if h.State == llmclient.HealthStateHealthy {
			continue
		}
		// Expired cooldowns are normalized to healthy and omitted — unless a
		// disable window still blocks the unit.
		inWindow, ok := windows[h.Provider]
		if h.State == llmclient.HealthStateCoolingDown && !ok && !now.Before(h.CooldownUntil) {
			continue
		}
		var cdUntil int64
		if h.State == llmclient.HealthStateCoolingDown {
			cdUntil = h.CooldownUntil.Unix()
		}
		state, reason, recovery := h.State, h.Reason, recoveryModeForState(h.State)
		var disableUntil int64
		if ok && state != llmclient.HealthStateDisabled {
			state = llmclient.HealthStateCoolingDown
			reason = disableWindowReason
			recovery = recoveryCooldown
			disableUntil = inWindow.deadline
			if disableUntil > cdUntil {
				cdUntil = disableUntil
			}
		}

		surfaced[k] = true
		items = append(items, domain.AICallableUnitView{
			ID:                  k,
			Model:               h.Model,
			ProviderName:        h.Provider,
			CooldownUntil:       cdUntil,
			DisableUntil:        disableUntil,
			CooldownReason:      reason,
			ConsecutiveFailures: int32(h.ConsecutiveFailures),
			HealthState:         state,
			HealthReason:        reason,
			RecoveryMode:        recovery,
		})
	}

	// Synthesize entries for disable-window providers whose units have no
	// failure record, so the composer's global-health overlay can mark every
	// blocked row instead of only the ones that also happened to fail.
	for provider, w := range windows {
		for _, m := range w.models {
			if surfaced[unitKey(provider, m.Name)] {
				continue
			}
			items = append(items, domain.AICallableUnitView{
				ID:            unitKey(provider, m.Name),
				Model:         m.Name,
				ProviderName:  provider,
				CooldownUntil: w.deadline,
				DisableUntil:  w.deadline,
				HealthState:   llmclient.HealthStateCoolingDown,
				HealthReason:  disableWindowReason,
				RecoveryMode:  recoveryCooldown,
			})
		}
	}

	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return domain.AIManagerUnitHealthListResp{Items: items}, nil
}
