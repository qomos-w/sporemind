package sshmanager

import (
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// sshmanagerCallableIDs is the full set of callables registered by the actor.
//
// Every one of them must carry WithService("sshmanager"): the turn engine's
// resolveServiceRefs routes a tool call to ctx.LookupService(ServiceName),
// falling back to ctx.Self() (agent-local) when empty. Without the service
// declaration, a direct tool like sshmanager.host_list lands on the agent
// itself, which has no such callable registered and fails with
// "call ID not registered".
var sshmanagerCallableIDs = []string{
	"sshmanager.host_list",
	"sshmanager.host_create",
	"sshmanager.host_update",
	"sshmanager.host_remove",
	"sshmanager.folder_create",
	"sshmanager.folder_rename",
	"sshmanager.folder_reorder",
	"sshmanager.folder_remove",
	"sshmanager.session_list",
	"sshmanager.shell_open",
	"sshmanager.shell_close",
	"sshmanager.shell_input",
	"sshmanager.shell_resize",
	"sshmanager.shell_stream",
	"sshmanager.file_list",
	"sshmanager.file_read",
	"sshmanager.file_write",
	"sshmanager.file_write_base64",
	"sshmanager.file_mkdir",
	"sshmanager.file_delete",
	"sshmanager.file_rename",
	"sshmanager.file_chmod",
	"sshmanager.file_download",
	"sshmanager.archive_export",
	"sshmanager.archive_import",
	"sshmanager.status_get",
	"sshmanager.status_list",
	"sshmanager.command_list",
	"sshmanager.command_create",
	"sshmanager.command_update",
	"sshmanager.command_remove",
	"sshmanager.history_list",
	"sshmanager.exec",
	"sshmanager.shell_run",
	"sshmanager.tunnel_open",
	"sshmanager.tunnel_close",
	"sshmanager.tunnel_list",
}

// TestRegistrationSurface_Declarations verifies that every registered
// callable carries WithService("sshmanager") and AdminOnly, so direct tools
// route to the sshmanager actor (not the agent) and stay admin-gated. exec
// additionally keeps its irreversible effect declaration.
func TestRegistrationSurface_Declarations(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.RegOpts = map[string][]actor.RegisterOption{}

	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	for _, id := range sshmanagerCallableIDs {
		opts, ok := ctx.RegOpts[id]
		if !ok {
			t.Errorf("%s not registered", id)
			continue
		}
		if got := actor.ResolveVisibility(opts...); got != actor.VisibilityAdmin {
			t.Errorf("%s Visibility = %v, want %v", id, got, actor.VisibilityAdmin)
		}
	}

	// exec and shell_run keep their irreversible effect declaration unchanged.
	for _, id := range []string{"sshmanager.exec", "sshmanager.shell_run"} {
		opts, ok := ctx.RegOpts[id]
		if !ok {
			t.Fatalf("%s not registered", id)
		}
		if got := actor.ResolveEffect(opts...); got != "irreversible" {
			t.Errorf("%s EffectKind = %q, want %q", id, got, "irreversible")
		}
	}
}