package agent

// The per-actor registration_surface_test.go files in each actor package
// (project, filesystem, shell, workspace, browsermanager, computeruse,
// appmanager, pluginhost, sshmanager) serve as the sole verification that
// every callable declares its EffectKind and ServiceName at the registration
// site. The agent-level heuristic tables have been removed; no snapshot test
// is needed here.
