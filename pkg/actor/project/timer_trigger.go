package project

import (
	"fmt"

	"github.com/qomos-w/gospore/actor"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func (a *Actor) handleTriggerTimerCard(ctx actor.PureContext, req gen.WikiTriggerTimerCardReq) (gen.WikiTriggerTimerCardResp, error) {
	if req.ID == "" {
		return gen.WikiTriggerTimerCardResp{}, fmt.Errorf("project.wiki.trigger_timer_card: card id is required")
	}
	card, err := a.store.Get(req.ID)
	if err != nil {
		return gen.WikiTriggerTimerCardResp{}, fmt.Errorf("project.wiki.trigger_timer_card: %w", err)
	}
	if !containsString(card.Tags, "scheduler") && !hasScheduleData(card.Data) {
		return gen.WikiTriggerTimerCardResp{}, fmt.Errorf("project.wiki.trigger_timer_card: card is not scheduled")
	}
	if err := a.handleExecuteTimerCard(ctx, gen.ProjectExecuteTimerCardReq{CardID: req.ID, ProjectID: a.actorID}); err != nil {
		return gen.WikiTriggerTimerCardResp{}, err
	}
	return gen.WikiTriggerTimerCardResp{ID: req.ID}, nil
}

func hasScheduleData(data map[string]any) bool {
	schedule, ok := data["schedule"].(map[string]any)
	return ok && (stringValue(schedule["cron"]) != "" || stringValue(schedule["expression"]) != "")
}
