package domain

import "path/filepath"

// GeneratedAssetsDir is the project-relative directory (slash-separated, with
// trailing slash) where media-generation callables (generate_image /
// generate_video) persist their artifacts inside the agent's bound project
// root. It is the single source of truth for both the writer (agent actor) and
// the validator that gates staged assets (puppeteditor agent surface).
const GeneratedAssetsDir = "assets/generated/"

// GeneratedAssetsPath joins the generated-assets directory onto a project root,
// producing the absolute directory artifacts are written to.
func GeneratedAssetsPath(projectRoot string) string {
	return filepath.Join(projectRoot, filepath.FromSlash(GeneratedAssetsDir))
}
