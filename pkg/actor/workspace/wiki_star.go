package workspace

import (
	"context"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// handleWikiListStarred aggregates the knowledge-base star list across every
// mounted project: for each mount it forwards project.wiki_get_starred to the
// project actor and tags each starred card id with its owning project, so the
// global starred quick-view renders the full cross-project list in one round
// trip.
//
// PureContext (stateless): the N sequential cross-actor forwards run on a
// forked goroutine, never serialized on the workspace owner lane (Owner Lane
// 禁阻塞). Each forward is bounded by systemWikiForwardTimeout. A mount whose
// project actor is not currently running (or whose forward fails) is skipped
// rather than failing the whole query — an unreachable project must not blank
// the global view. Best-effort: skipped mounts are logged.
//
// The workspace's internal system meta project (`System: true`, the mount that
// holds builtin cards) is skipped: it must never appear in the user-facing
// global knowledge base, mirroring the front-end enumeration policy
// (`kbData.listKbProjects`).
func (a *Actor) handleWikiListStarred(ctx actor.PureContext, _ domain.WikiListStarredReq) (domain.WikiListStarredResp, error) {
	mounts := a.mountsSnapshot()
	items := make([]domain.WikiStarredEntry, 0)
	for _, m := range mounts {
		if m.System {
			continue
		}
		if m.ActorID == "" {
			continue
		}
		cid, err := identity.ParseCanonicalID(m.ActorID)
		if err != nil {
			ctx.Logger().Warn("workspace: wiki_list_starred: invalid project actor id",
				"project", m.Name, "actorID", m.ActorID, "error", err)
			continue
		}
		projRef, ok := ctx.LookupID(id.From(cid))
		if !ok || projRef == nil {
			continue
		}
		forwardCtx, cancel := context.WithTimeout(ctx.Lifecycle(), systemWikiForwardTimeout)
		call := projRef.Invoke(forwardCtx, "project.wiki_get_starred", domain.WikiGetStarredReq{})
		if call == nil {
			cancel()
			ctx.Logger().Warn("workspace: wiki_list_starred: nil call", "project", m.Name)
			continue
		}
		raw, err := call.Final(forwardCtx)
		cancel()
		if err != nil {
			ctx.Logger().Warn("workspace: wiki_list_starred: forward failed",
				"project", m.Name, "error", err)
			continue
		}
		resp, ok := raw.(domain.WikiStarredResp)
		if !ok {
			ctx.Logger().Warn("workspace: wiki_list_starred: unexpected response type",
				"project", m.Name, "type", raw)
			continue
		}
		for _, cardID := range resp.Starred {
			items = append(items, domain.WikiStarredEntry{
				ProjectID:   m.ActorID,
				ProjectName: m.Name,
				CardID:      cardID,
			})
		}
	}
	return domain.WikiListStarredResp{Items: items}, nil
}
