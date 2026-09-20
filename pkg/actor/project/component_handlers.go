package project

import (
	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
)

func (a *Actor) handleComponentList(ctx actor.PureContext, _ domain.ProjectComponentListReq) (domain.ProjectComponentListResp, error) {
	items, err := a.listComponentDescriptors(ctx)
	return domain.ProjectComponentListResp{Items: items}, err
}

func (a *Actor) handleComponentGet(ctx actor.PureContext, req domain.ProjectComponentGetReq) (domain.ProjectComponentGetResp, error) {
	component, err := a.componentDescriptor(ctx, req.CardID)
	return domain.ProjectComponentGetResp{Component: component}, err
}
