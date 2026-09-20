package appbinding

import "path/filepath"

// AppDirFromArtifactPath derives the app directory from a native build
// artifact path. The build pipeline emits subprocess executables at
// <appDir>/.sporecode/build/plugin-*.exe; the plugin process inherits the
// host's cwd, so the SDK's static "/" route must be anchored explicitly —
// the 2026-09 run-directory leak (directory listing of the host cwd through
// the gateway). A path that does not match the three-level layout yields ""
// and the SDK stays on its fail-closed default.
//
// Shared by appmanager (OnLoad config StaticDir) and pluginhost (generated
// media placement: artifacts written into <appDir>/media/ are served
// same-origin by the plugin's own listener) so both derive the same root.
func AppDirFromArtifactPath(artifactPath string) string {
	if artifactPath == "" {
		return ""
	}
	buildDir := filepath.Dir(artifactPath) // <appDir>/.sporecode/build
	sporeDir := filepath.Dir(buildDir)     // <appDir>/.sporecode
	appDir := filepath.Dir(sporeDir)       // <appDir>
	if filepath.Base(buildDir) != "build" || filepath.Base(sporeDir) != ".sporecode" || appDir == sporeDir {
		// Not the known layout — refuse to guess a root.
		return ""
	}
	return appDir
}
