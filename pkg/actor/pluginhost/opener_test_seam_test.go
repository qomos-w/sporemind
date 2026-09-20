package pluginhost

import (
	"github.com/qomos-w/sporemind/pkg/pluginhost"
)

// SetOpenerOverrideForTest installs a stub ArtifactOpener on a pluginhost
// Actor so matrix e2e tests (external test package pluginhost_test) can boot
// the actor inside a real gospel runtime without real shared libraries or
// child processes. Production code never sets openerOverride; OnStart
// prefers it only when non-nil (mirroring the goBuildOverride seam).
func SetOpenerOverrideForTest(a *Actor, opener pluginhost.ArtifactOpener) {
	a.openerOverride = opener
}
