package appmanager

import (
	"github.com/qomos-w/sporemind/pkg/codegen"
)

// bundleDepsSnapshot copies every registered app's manifest, schema
// descriptors and package hash for the codegen bundle-call extractor. It is
// the registry view dev_generate types bundle-level permissions against; the
// drift gate (dev_gate) re-derives it and compares against the persisted
// generation snapshots.
func (a *Actor) bundleDepsSnapshot() map[string]codegen.BundleDep {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[string]codegen.BundleDep, len(a.Apps))
	for id, manifest := range a.Apps {
		dep := codegen.BundleDep{Manifest: manifest}
		if rec, ok := a.Records[id]; ok {
			dep.Descriptors = rec.SchemaDescriptors
			dep.PackageHash = rec.PackageHash
		}
		out[id] = dep
	}
	return out
}
