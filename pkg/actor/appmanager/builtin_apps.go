package appmanager

import (
	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/builtin/sporecall"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// ensureBuiltinSporeApps runs at the tail of OnStart, after persisted records
// are restored: it guarantees the builtin spore bundles (sporecall — the
// spore-script → host-callable relay) exist as registered apps so their
// app-bundle cards are always present in the optional-mount catalog.
// Registration alone grants nothing: agent tool exposure is mount-gated
// (agent_config.go appToolsAllowedByBundleMount — no mounted bundle card =
// zero tools), so this only ever creates the option to mount.
//
// Per-record policy:
//   - no record → register (first boot, or the operator unregistered it and
//     the next boot restores the builtin default);
//   - live record with the same canonical package hash → skip (steady-state
//     boots never respawn the child);
//   - builtin-origin record with a drifted hash (host upgrade changed the
//     builtin source) → full unregister, then fresh register. The direct
//     replace path cannot be used: spawnChild does not stop a live spore
//     child, so re-registering over one fails with tree "name taken";
//   - a user/project-origin record squatting the ID outranks builtin
//     (checkOriginOverride would reject the replacement anyway) → leave it.
func (a *Actor) ensureBuiltinSporeApps(ctx actor.Context) {
	for _, req := range builtinSporeRegisterReqs() {
		a.ensureBuiltinSporeApp(ctx, req)
	}
}

func builtinSporeRegisterReqs() []gen.AppManagerRegisterReq {
	return []gen.AppManagerRegisterReq{sporecall.RegisterReq()}
}

func (a *Actor) ensureBuiltinSporeApp(ctx actor.Context, req gen.AppManagerRegisterReq) {
	wantHash, err := canonicalPackageHash(req.Manifest, req.EntryModule, req.Modules, req.Assets, req.SchemaDescriptors, req.Abi, req.ArtifactHash)
	if err != nil {
		ctx.Logger().Error("appmanager: builtin app hash failed", "id", req.Manifest.ID, "error", err)
		return
	}
	action := "register"
	var record appRecord
	a.withMu(func() {
		var exists bool
		record, exists = a.Records[req.Manifest.ID]
		switch {
		case !exists:
			action = "register"
		case record.PackageHash == wantHash && recordLive(record.State):
			action = "skip"
		case normalizeOrigin(record.Origin) != "builtin":
			action = "skip"
		default:
			action = "replace"
		}
	})
	switch action {
	case "skip":
		return
	case "replace":
		if err := a.handleUnregister(ctx, gen.AppManagerUnregisterReq{ID: req.Manifest.ID}); err != nil {
			ctx.Logger().Error("appmanager: builtin app upgrade unregister failed", "id", req.Manifest.ID, "error", err)
			return
		}
	}
	if _, err := a.doRegister(ctx, req, false); err != nil {
		ctx.Logger().Error("appmanager: builtin app registration failed", "id", req.Manifest.ID, "error", err)
	}
}
