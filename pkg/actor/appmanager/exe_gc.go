package appmanager

import (
	"os"
	"path/filepath"
	"strings"
)

// gcSupersededArtifacts deletes content-addressed plugin executables that the
// given current artifact path supersedes: every sibling in its build dir
// matching the app's sweep prefix that is not referenced by any live record.
// Dev builds are content-addressed (plugin-<name>-<version>-<hash><ext>) and
// every rebuild lands on a new file — without this sweep the .sporecode/build
// dir grows by one binary per reload forever. sweepPrefix must be
// pluginhost.PluginArtifactSweepPrefix(manifest name) so a version bump still
// sweeps the previous version's binaries; when empty (legacy records) the
// prefix is derived from the keep path instead. Files still locked by a
// running process (Windows keeps a mapped exe undeletable) fail on remove and
// are skipped; the orphan reaper clears those on the next host start and the
// next sweep retries. Returns the number of files removed.
func (a *Actor) gcSupersededArtifacts(keepPath, sweepPrefix string) int {
	if keepPath == "" {
		return 0
	}
	dir := filepath.Dir(keepPath)
	keepBase := filepath.Base(keepPath)
	prefix := sweepPrefix
	if prefix == "" {
		dash := strings.LastIndex(keepBase, "-")
		if dash <= 0 {
			return 0
		}
		prefix = keepBase[:dash+1] // "plugin-<owner>-"
	} else if !strings.HasPrefix(keepBase, prefix) {
		return 0
	}

	// Sibling apps of the same project share the build dir (the prefix owner
	// is the project id): never delete a file another live record references.
	keep := map[string]bool{keepBase: true}
	a.withMu(func() {
		for _, r := range a.Records {
			if r.ArtifactPath == "" {
				continue
			}
			if filepath.Dir(r.ArtifactPath) == dir {
				keep[filepath.Base(r.ArtifactPath)] = true
			}
		}
	})

	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	removed := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || keep[name] || !strings.HasPrefix(name, prefix) {
			continue
		}
		switch strings.ToLower(filepath.Ext(name)) {
		case ".exe", ".dll":
		default:
			continue
		}
		if os.Remove(filepath.Join(dir, name)) == nil {
			removed++
		}
	}
	return removed
}
