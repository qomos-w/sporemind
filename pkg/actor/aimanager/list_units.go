package aimanager

import (
	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// handleListUnits returns every selectable unit (model, provider) pair across
// all registered LLM providers, each annotated with its runtime modality and
// health projection. This is the unit-centric counterpart to model_list (bare
// models) and provider_list (provider-centric): the caller gets
// ModelUnit-equivalent tuples ready for selection, optionally filtered by
// modality. All units are included regardless of health state — disabled and
// cooling_down units are visible so the user can manually select them,
// mirroring aggregator pools.
func (a *Actor) handleListUnits(_ actor.PureContext, req domain.AIManagerListUnitsReq) (domain.AIManagerListUnitsResp, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	units := a.resolveUnitsWithCooldown(a.buildAutoUnits())
	if req.Modality == "" {
		return domain.AIManagerListUnitsResp{Items: units}, nil
	}
	out := make([]domain.ManualCallableUnit, 0, len(units))
	for _, u := range units {
		if u.Modality == req.Modality {
			out = append(out, u)
		}
	}
	return domain.AIManagerListUnitsResp{Items: out}, nil
}
