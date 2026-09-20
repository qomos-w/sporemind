package sporecall

import (
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// RegisterReq builds an AppManagerRegisterReq for the sporecall bundle,
// suitable for passing to appmanager.register in tests or manual runs.
func RegisterReq() gen.AppManagerRegisterReq {
	manifest := Manifest
	manifest.Callables = append([]gen.AppCallableDescriptor(nil), Manifest.Callables...)
	return gen.AppManagerRegisterReq{
		Manifest:    manifest,
		EntryModule: EntryModule,
		Modules:     Modules,
		Origin:      "builtin",
	}
}
