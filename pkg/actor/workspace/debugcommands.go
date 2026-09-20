package workspace

import (
	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/debugcmd"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// handleListDebugCommands returns all registered debug commands so the
// frontend debug console can render the command menu. Discovery mirrors
// handleListSlashCommands; execution flows through handleExecDebugCommand ->
// debugcmd.Dispatch.
func (a *Actor) handleListDebugCommands(actor.PureContext) (domain.DebugCommandsListResp, error) {
	cmds := debugcmd.All()
	out := make([]domain.SlashCommand, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, domain.SlashCommand{Name: c.Name(), ShortHelp: c.ShortHelp()})
	}
	return domain.DebugCommandsListResp{Commands: out}, nil
}

// handleExecDebugCommand executes a registered debug command with pre-split
// args. The frontend console performs the quote-aware arg splitting before
// the wire (regex "([^"]*)"|(\S+), go-ebitor core_debug.go pattern); the
// backend dispatches on the args as-is and never re-parses an input line.
// Unknown command names surface as ErrUnknownCommand from debugcmd.Dispatch.
func (a *Actor) handleExecDebugCommand(ctx actor.PureContext, req domain.DebugCommandExecReq) (domain.DebugCommandExecResp, error) {
	output, err := debugcmd.Dispatch(ctx.Lifecycle(), req.Name, req.Args)
	if err != nil {
		return domain.DebugCommandExecResp{}, err
	}
	return domain.DebugCommandExecResp{Output: output}, nil
}
