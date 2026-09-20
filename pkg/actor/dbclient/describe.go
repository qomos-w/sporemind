package dbclient

import (
	"context"
	"fmt"
	"strings"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/policy"
)

// hasDescribe is the optional table-structure surface (mysql / postgres).
// The actor type-asserts it like hasObjectStorage so non-sql backends get a
// clear "not supported" error instead of dispatching through a stub.
type hasDescribe interface {
	Describe(ctx context.Context, path string) (domain.DbDescribeResp, error)
}

// handleDescribe returns columns + indexes for one table ("db.table" path,
// same shape as tree/read). Only sql backends implement the surface.
func (a *Actor) handleDescribe(ctx actor.Context, req domain.DbDescribeReq) (domain.DbDescribeResp, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.DbDescribeResp{}, err
	}
	if strings.TrimSpace(req.ProfileID) == "" {
		return domain.DbDescribeResp{}, fmt.Errorf("dbclient.describe: profileId is required")
	}
	if strings.TrimSpace(req.Path) == "" {
		return domain.DbDescribeResp{}, fmt.Errorf("dbclient.describe: path (db.table) is required")
	}
	p, err := a.lookupProfile(ctx, req.ProfileID)
	if err != nil {
		return domain.DbDescribeResp{}, err
	}
	conn, err := a.getConn(ctx, p, dbKeyFor(p, req.Path))
	if err != nil {
		return domain.DbDescribeResp{}, err
	}
	desc, ok := conn.(hasDescribe)
	if !ok {
		return domain.DbDescribeResp{}, fmt.Errorf("dbclient.describe: backend %q does not support table structure", p.Backend)
	}
	cctx, cancel := context.WithTimeout(ctx.Lifecycle(), queryTimeout)
	defer cancel()
	return desc.Describe(cctx, req.Path)
}
