package agent

import (
	"context"
	"fmt"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// handleOpenSshSession opens an interactive SSH shell session on the host
// identified by HostId. It first asks sshmanager for an existing connected
// session on the same host (sshmanager.session_list) and reuses it: the reuse
// path returns the existing SessionId without calling shell_open, so no
// open_session broadcast is emitted and the frontend does not open a second
// terminal tab. Only when no reusable session exists does it call
// sshmanager.shell_open, which creates the session and emits an
// ssh_manager_event (kind "open_session") so the desktop frontend opens a
// right-panel ssh-session tab with a live xterm.js terminal. The agent
// receives the SessionId for subsequent shell_input / shell_stream calls.
// In headless mode a newly opened session is created but no frontend tab
// appears.
func (a *Actor) handleOpenSshSession(ctx actor.PureContext, req gen.AgentOpenSshSessionReq) (gen.AgentOpenSshSessionResp, error) {
	if req.HostID == "" {
		return gen.AgentOpenSshSessionResp{Error: "open_ssh_session: hostId is required"}, nil
	}

	planner := ctx.Planner()
	if planner == nil {
		return gen.AgentOpenSshSessionResp{Error: "open_ssh_session: planner not available"}, nil
	}
	mgrRef, ok := ctx.LookupService("sshmanager")
	if !ok {
		return gen.AgentOpenSshSessionResp{Error: "open_ssh_session: sshmanager service not available"}, nil
	}

	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()

	// Reuse an existing connected session on the host: opening a duplicate
	// would re-emit the open_session broadcast and make the frontend open a
	// second terminal tab for the same host.
	result, err := planner.Call(callCtx, mgrRef, "sshmanager.session_list", domain.SshSessionListReq{}).Await()
	if err != nil {
		return gen.AgentOpenSshSessionResp{Error: fmt.Sprintf("open_ssh_session: %v", err)}, nil
	}
	listResp, ok := result.(domain.SshSessionListResp)
	if !ok {
		return gen.AgentOpenSshSessionResp{Error: "open_ssh_session: unexpected response from sshmanager"}, nil
	}
	for _, s := range listResp.Items {
		if s.HostID == req.HostID && s.Connected {
			return gen.AgentOpenSshSessionResp{
				SessionID: s.SessionID,
				HostID:    req.HostID,
				Connected: true,
			}, nil
		}
	}

	// No reusable session — open a new one.
	result, err = planner.Call(callCtx, mgrRef, "sshmanager.shell_open", domain.SshShellOpenReq{HostID: req.HostID}).Await()
	if err != nil {
		return gen.AgentOpenSshSessionResp{Error: fmt.Sprintf("open_ssh_session: %v", err)}, nil
	}
	resp, ok := result.(domain.SshShellOpenResp)
	if !ok {
		return gen.AgentOpenSshSessionResp{Error: "open_ssh_session: unexpected response from sshmanager"}, nil
	}

	return gen.AgentOpenSshSessionResp{
		SessionID: resp.SessionID,
		HostID:    req.HostID,
		Connected: resp.Connected,
		Error:     resp.Error,
	}, nil
}
