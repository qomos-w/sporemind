package project

import (
	"fmt"

	"github.com/qomos-w/gospore/actor"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/policy"
)

func (a *Actor) handleProjectCardMount(ctx actor.PureContext, req gen.ProjectCardMountReq) (gen.ProjectCardMountResp, error) {
	if err := policy.RequireDeveloper(ctx.Identity().Role); err != nil {
		return gen.ProjectCardMountResp{}, fmt.Errorf("project.card.mount: %w", err)
	}
	if req.Ref.ID == "" {
		return gen.ProjectCardMountResp{}, fmt.Errorf("card ref: card id is required")
	}
	if req.Ref.Scope != "" && req.Ref.Scope != "project" {
		return gen.ProjectCardMountResp{}, fmt.Errorf("card ref %q has invalid project scope %q", req.Ref.ID, req.Ref.Scope)
	}
	req.Ref.Scope = "project"
	if _, err := a.cardRecord(ctx, req.Ref.ID); err != nil {
		return gen.ProjectCardMountResp{}, err
	}
	if err := a.validateCardDependencies(ctx, req.Ref.ID, map[string]bool{}); err != nil {
		return gen.ProjectCardMountResp{}, err
	}
	// Duplicate detection and the append live in the same serialized card
	// read-modify-write so concurrent mounts of one card cannot double-append.
	var mountedRef gen.CardRef
	err := a.updateConfigCard(func(c *configCard) {
		for _, ref := range c.MountedCardRefs {
			if ref.ID == req.Ref.ID {
				mountedRef = ref
				return
			}
		}
		c.MountedCardRefs = append(c.MountedCardRefs, req.Ref)
		mountedRef = req.Ref
	})
	if err != nil {
		return gen.ProjectCardMountResp{}, err
	}
	return gen.ProjectCardMountResp{Ref: mountedRef}, nil
}

func (a *Actor) handleProjectCardUnmount(ctx actor.PureContext, req gen.ProjectCardUnmountReq) (gen.ProjectCardUnmountResp, error) {
	if err := policy.RequireDeveloper(ctx.Identity().Role); err != nil {
		return gen.ProjectCardUnmountResp{}, fmt.Errorf("project.card.unmount: %w", err)
	}
	if req.ID == "" {
		return gen.ProjectCardUnmountResp{}, fmt.Errorf("card ref: card id is required")
	}
	found := false
	err := a.updateConfigCard(func(c *configCard) {
		for i, ref := range c.MountedCardRefs {
			if ref.ID == req.ID {
				c.MountedCardRefs = append(c.MountedCardRefs[:i], c.MountedCardRefs[i+1:]...)
				found = true
				return
			}
		}
	})
	if err != nil {
		return gen.ProjectCardUnmountResp{}, err
	}
	if !found {
		return gen.ProjectCardUnmountResp{}, fmt.Errorf("card ref %q is not mounted", req.ID)
	}
	return gen.ProjectCardUnmountResp{ID: req.ID}, nil
}

func (a *Actor) handleProjectCardList(_ actor.PureContext, _ gen.ProjectCardListReq) (gen.ProjectCardListResp, error) {
	refs, err := a.mountedCardRefsSnapshot()
	if err != nil {
		return gen.ProjectCardListResp{}, err
	}
	return gen.ProjectCardListResp{Refs: refs}, nil
}

func (a *Actor) cardRecord(ctx actor.PureContext, id string) (*CardRecord, error) {
	if a.store == nil {
		return nil, fmt.Errorf("card store is unavailable")
	}
	if record, err := a.store.Get(id); err == nil {
		return record, nil
	}
	if provider := a.externalProviderFor(id); provider != nil {
		raw, err := provider.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		return decodeCard(id, raw), nil
	}
	return nil, fmt.Errorf("card %q not found", id)
}

func (a *Actor) validateCardDependencies(ctx actor.PureContext, id string, visiting map[string]bool) error {
	if visiting[id] {
		return fmt.Errorf("card dependency cycle at %q", id)
	}
	visiting[id] = true
	defer delete(visiting, id)
	record, err := a.cardRecord(ctx, id)
	if err != nil {
		return err
	}
	descriptor, ok, err := componentDescriptorFromCard(record)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	for _, dep := range descriptor.Dependencies {
		if dep.Required {
			if err := a.validateCardDependencies(ctx, dep.CardID, visiting); err != nil {
				return err
			}
		}
	}
	return nil
}
