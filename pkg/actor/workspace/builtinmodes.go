package workspace

import (
	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/agentkit"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// handleListBuiltinModes returns every builtin mode card (builtin:mode:*) so the
// frontend conversation composer can preload a static slash-mode interception
// filter at startup. Typing "/<name>" mounts the matching mode without
// submitting; mounting itself flows through agent.component.mount.
func (a *Actor) handleListBuiltinModes(actor.PureContext) (domain.WorkspaceBuiltinModesListResp, error) {
	cards, err := agentkit.BuiltinModeCards()
	if err != nil {
		return domain.WorkspaceBuiltinModesListResp{}, err
	}
	modes := make([]domain.BuiltinMode, 0, len(cards))
	for _, c := range cards {
		modes = append(modes, domain.BuiltinMode{CardID: c.CardID, Name: c.Name, Title: c.Title, Icon: c.Icon})
	}
	return domain.WorkspaceBuiltinModesListResp{Modes: modes}, nil
}
