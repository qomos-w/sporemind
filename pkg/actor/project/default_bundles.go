package project

import (
	"fmt"

	"github.com/qomos-w/gospore/actor"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// defaultExtraBundlesSnapshot returns a private copy of the project's default
// extra bundle IDs, read fresh from the config card (the ground truth; there
// is no in-memory authoritative copy). Used by spawn_agent to merge the
// defaults into the request's ExtraBundleIDs before constructing agent props.
func (a *Actor) defaultExtraBundlesSnapshot() ([]string, error) {
	c, err := a.configSnapshot()
	if err != nil {
		return nil, err
	}
	return c.DefaultExtraBundleIDs, nil
}

// setDefaultExtraBundles replaces the config card's default extra bundle list
// (thread-safe via configMu). The write is durable immediately.
func (a *Actor) setDefaultExtraBundles(ids []string) error {
	return a.updateConfigCard(func(c *configCard) {
		c.DefaultExtraBundleIDs = ids
	})
}

// mergeExtraBundleIDs merges project defaults with caller-supplied extras:
// defaults first, extras after, first occurrence wins (dedup). When no
// defaults are configured the extras slice is returned untouched (spawn
// behavior identical to the pre-merge path). Nil-safe on both sides.
func mergeExtraBundleIDs(defaults, extras []string) []string {
	if len(defaults) == 0 {
		return extras
	}
	out := make([]string, 0, len(defaults)+len(extras))
	seen := make(map[string]struct{}, len(defaults)+len(extras))
	for _, id := range defaults {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for _, id := range extras {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// handleDefaultBundlesGet reports the project's default extra bundle IDs.
func (a *Actor) handleDefaultBundlesGet(_ actor.PureContext, _ gen.ProjectDefaultBundlesGetReq) (gen.ProjectDefaultBundlesGetResp, error) {
	ids, err := a.defaultExtraBundlesSnapshot()
	if err != nil {
		return gen.ProjectDefaultBundlesGetResp{}, fmt.Errorf("project.default_bundles_get: read config card: %w", err)
	}
	return gen.ProjectDefaultBundlesGetResp{BundleIDs: ids}, nil
}

// handleDefaultBundlesSet replaces the project's default extra bundle list.
// Applies to agents spawned afterwards only; existing agents are untouched.
func (a *Actor) handleDefaultBundlesSet(_ actor.PureContext, req gen.ProjectDefaultBundlesSetReq) (gen.ProjectDefaultBundlesSetResp, error) {
	ids := make([]string, 0, len(req.BundleIDs))
	seen := make(map[string]struct{}, len(req.BundleIDs))
	for _, id := range req.BundleIDs {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if err := a.setDefaultExtraBundles(ids); err != nil {
		return gen.ProjectDefaultBundlesSetResp{}, fmt.Errorf("project.default_bundles_set: update config card: %w", err)
	}
	return gen.ProjectDefaultBundlesSetResp{BundleIDs: ids}, nil
}
