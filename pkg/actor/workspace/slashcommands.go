package workspace

import (
	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/slashcmd"
)

// handleListSlashCommands returns all registered slash commands so the frontend
// conversation composer can render a command menu when the user types '/'.
// Execution still flows through agent.chat.submit -> slashcmd.Dispatch; this
// callable is read-only discovery only.
func (a *Actor) handleListSlashCommands(actor.PureContext) (domain.WorkspaceSlashCommandsListResp, error) {
	cmds := slashcmd.All()
	out := make([]domain.SlashCommand, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, domain.SlashCommand{Name: c.Name(), ShortHelp: c.ShortHelp()})
	}
	return domain.WorkspaceSlashCommandsListResp{Commands: out}, nil
}
