package appbinding

// Workspace mount discovery. One read-only callID aliased to the workspace
// actor's mount-table snapshot: a plugin (e.g. a code-map visualizer that
// needs a project root to scan) lists the host-mounted projects with their
// names, root paths, sub-mounts, and permission modes. Read-only by
// construction — mounts are added/removed only through the AdminOnly
// workspace.mount / workspace.unmount / workspace.add_mount / remove_mount
// callables, which are intentionally NOT exposed here.
func init() {
	RegisterCapability(CapabilityDef{
		ID:                 CapWorkspaceRead,
		Title:              "读取工作区挂载列表",
		Description:        "允许应用读取宿主工作区已挂载的项目列表（名称、根路径、子挂载、权限模式），只读。",
		RiskLevel:          RiskLow,
		I18nTitleKey:       "capability.workspace.read.title",
		I18nDescriptionKey: "capability.workspace.read.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "读取工作区挂载列表", Description: "允许应用读取宿主工作区已挂载的项目列表（名称、根路径、子挂载、权限模式），只读。"},
			"en-US": {Title: "Read workspace mount list", Description: "Allows the app to read the host workspace's mounted project list (names, root paths, sub-mounts, permission modes), read-only."},
		},
	})
	RegisterHostCall(HostCallDef{
		CallID:     "workspace.list_projects",
		Capability: CapWorkspaceRead,
		Target:     "workspace.list_project",
	})
}
