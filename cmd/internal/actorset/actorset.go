// Package actorset declares the standard sporemind top-level actor set used by
// the cmd/* binaries. Keeping it in one place ensures sporemind, sporemind-desktop,
// and sporemind-gen-manifest spawn the same tree.
package actorset

import (
	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/actor/aimanager"
	"github.com/qomos-w/sporemind/pkg/actor/aistats"
	"github.com/qomos-w/sporemind/pkg/actor/appmanager"
	"github.com/qomos-w/sporemind/pkg/actor/browsermanager"
	"github.com/qomos-w/sporemind/pkg/actor/cloudaccount"
	"github.com/qomos-w/sporemind/pkg/actor/computeruse"
	"github.com/qomos-w/sporemind/pkg/actor/cookiebridge"
	"github.com/qomos-w/sporemind/pkg/actor/crawl"
	"github.com/qomos-w/sporemind/pkg/actor/dbclient"
	"github.com/qomos-w/sporemind/pkg/actor/dbmanager"
	"github.com/qomos-w/sporemind/pkg/actor/filesystem"
	"github.com/qomos-w/sporemind/pkg/actor/frpmanager"
	"github.com/qomos-w/sporemind/pkg/actor/glassinteract"
	"github.com/qomos-w/sporemind/pkg/actor/im"
	"github.com/qomos-w/sporemind/pkg/actor/interfacemanager"
	"github.com/qomos-w/sporemind/pkg/actor/lspserver"
	"github.com/qomos-w/sporemind/pkg/actor/mcpmanager"
	"github.com/qomos-w/sporemind/pkg/actor/media"
	"github.com/qomos-w/sporemind/pkg/actor/oracle"
	"github.com/qomos-w/sporemind/pkg/actor/pluginhost"
	"github.com/qomos-w/sporemind/pkg/actor/puppeteditor"
	"github.com/qomos-w/sporemind/pkg/actor/scheduler"
	"github.com/qomos-w/sporemind/pkg/actor/shell"
	"github.com/qomos-w/sporemind/pkg/actor/sshmanager"
	"github.com/qomos-w/sporemind/pkg/actor/storeclient"
	"github.com/qomos-w/sporemind/pkg/actor/toast"
	"github.com/qomos-w/sporemind/pkg/actor/user"
	"github.com/qomos-w/sporemind/pkg/actor/voice"
	"github.com/qomos-w/sporemind/pkg/actor/websearch"
	"github.com/qomos-w/sporemind/pkg/actor/workbench"
	"github.com/qomos-w/sporemind/pkg/actor/workspace"
	"github.com/qomos-w/sporemind/pkg/runtime"
)

// Default returns the canonical sporemind child actor set.
func Default() []runtime.ChildSpec {
	return defaultSet(false)
}

// ForManifest returns the actor set used by the manifest generator. It
// disables the system plugin project so the exported manifest does not
// contain duplicate projections from the prototype project and the system
// project.
func ForManifest() []runtime.ChildSpec {
	return defaultSet(true)
}

func defaultSet(noSystemProject bool) []runtime.ChildSpec {
	return []runtime.ChildSpec{
		{Name: "workspace", Factory: func() actor.Actor { return &workspace.Actor{NoSystemProject: noSystemProject} }, RequirePersistent: true, Role: "system", Planner: true},
		{Name: "user", Factory: func() actor.Actor { return &user.Actor{} }, RequirePersistent: true, Role: "system", DependsOn: []string{"workspace"}},
		{Name: "toast", Factory: toast.NewActor(), RequirePersistent: false, Role: "system", DependsOn: []string{"user"}},
		{Name: "cloudaccount", Factory: func() actor.Actor { return cloudaccount.NewActor() }, RequirePersistent: true, Role: "system", DependsOn: []string{"user"}},
		{Name: "filesystem", Factory: func() actor.Actor { return &filesystem.Actor{} }, RequirePersistent: false, Role: "system", DependsOn: []string{"cloudaccount"}},
		{Name: "lspserver", Factory: func() actor.Actor { return &lspserver.Actor{} }, RequirePersistent: false, Role: "system", DependsOn: []string{"filesystem"}},
		{Name: "aimanager", Factory: func() actor.Actor { return &aimanager.Actor{} }, RequirePersistent: true, Role: "system", DependsOn: []string{"filesystem"}},
		{Name: "aistats", Factory: aistats.NewActor(), RequirePersistent: true, Role: "system", DependsOn: []string{"aimanager"}},
		{Name: "appmanager", Factory: func() actor.Actor { return appmanager.NewActor() }, RequirePersistent: true, Role: "system", Planner: true, DependsOn: []string{"aistats"}},
		{Name: "workbench", Factory: workbench.NewActor(), RequirePersistent: true, Role: "system", DependsOn: []string{"workspace", "appmanager"}},
		{Name: "browsermanager", Factory: func() actor.Actor { return &browsermanager.Actor{} }, RequirePersistent: true, Role: "system", Planner: true, DependsOn: []string{"appmanager"}},
		{Name: "storeclient", Factory: func() actor.Actor { return &storeclient.Actor{} }, RequirePersistent: true, Role: "system", Planner: true, DependsOn: []string{"appmanager", "cloudaccount"}},
		{Name: "cookiebridge", Factory: func() actor.Actor { return &cookiebridge.Actor{} }, RequirePersistent: true, Role: "system", DependsOn: []string{"browsermanager"}},
		{Name: "crawl", Factory: func() actor.Actor { return &crawl.Actor{} }, RequirePersistent: true, Role: "system", DependsOn: []string{"browsermanager"}},
		{Name: "oracle", Factory: func() actor.Actor { return &oracle.Actor{} }, RequirePersistent: false, Role: "system", Planner: true, DependsOn: []string{"crawl"}},
		{Name: "pluginhost", Factory: func() actor.Actor { return &pluginhost.Actor{} }, RequirePersistent: true, Role: "system", Planner: true, DependsOn: []string{"oracle"}},
		{Name: "frpmanager", Factory: func() actor.Actor { return &frpmanager.Actor{} }, RequirePersistent: true, Role: "system", DependsOn: []string{"pluginhost"}},
		{Name: "mcpmanager", Factory: func() actor.Actor { return &mcpmanager.Actor{} }, RequirePersistent: true, Role: "system", DependsOn: []string{"frpmanager"}},
		{Name: "media", Factory: func() actor.Actor { return &media.Actor{} }, RequirePersistent: true, Role: "system", DependsOn: []string{"mcpmanager"}},
		{Name: "voice", Factory: func() actor.Actor { return &voice.Actor{} }, RequirePersistent: false, Role: "system", DependsOn: []string{"media"}},
		{Name: "websearch", Factory: func() actor.Actor { return &websearch.Actor{} }, RequirePersistent: true, Role: "system", DependsOn: []string{"voice"}},
		{Name: "shell", Factory: func() actor.Actor { return &shell.Actor{} }, RequirePersistent: false, Role: "system", DependsOn: []string{"websearch"}},
		{Name: "dbmanager", Factory: dbmanager.NewActor(), RequirePersistent: true, Role: "system", DependsOn: []string{"shell"}},
		// sshmanager precedes dbclient: it depends only on shell/appmanager, so
		// listing it before dbclient keeps Default() topologically canonical
		// (topo.Sort reproduces the declared order — TestDefaultSetOrderEquivalent).
		{Name: "sshmanager", Factory: func() actor.Actor { return &sshmanager.Actor{} }, RequirePersistent: true, Role: "system", Planner: true, DependsOn: []string{"shell", "appmanager"}},
		// Planner: dbclient dials through dbmanager.profile_lookup via
		// planner.Call from the dbclient_exec lane; without the capability
		// every profile lookup fails with "dbmanager service is not available".
		{Name: "dbclient", Factory: dbclient.NewActor(), RequirePersistent: false, Role: "system", Planner: true, DependsOn: []string{"dbmanager"}},
		{Name: "computeruse", Factory: func() actor.Actor { return &computeruse.Actor{} }, RequirePersistent: true, Role: "system", DependsOn: []string{"sshmanager"}},
		{Name: "glassinteract", Factory: func() actor.Actor { return &glassinteract.Actor{} }, RequirePersistent: true, Role: "system", Planner: true, DependsOn: []string{"computeruse"}},
		{Name: "interfacemanager", Factory: func() actor.Actor { return &interfacemanager.Actor{} }, RequirePersistent: true, Role: "system", DependsOn: []string{"glassinteract"}},
		{Name: "scheduler", Factory: func() actor.Actor { return &scheduler.Actor{} }, RequirePersistent: true, Role: "system", DependsOn: []string{"interfacemanager"}},
		{Name: "puppeteditor", Factory: func() actor.Actor { return &puppeteditor.Actor{} }, RequirePersistent: true, Role: "system", DependsOn: []string{"scheduler"}},
		{Name: "im", Factory: func() actor.Actor { return &im.Actor{} }, RequirePersistent: false, Role: "system", Planner: true, DependsOn: []string{"puppeteditor"}},
	}
}
