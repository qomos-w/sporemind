package pluginhost

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/pluginhost"
)

// selectTransport is the pure host-side transport decision for one native
// artifact load (task card T7). It answers "inprocess (FFI/purego loaderOpener)
// or subprocess (processOpener)" from the manifest signals:
//
//   - manifest.Isolation (T2 schema value): the declared transport intent,
//     validated by pkg/pluginhost/contract.go to be inprocess|subprocess.
//     This is the primary signal — a plugin built for subprocess loads via
//     the subprocess transport on every host (dev or prod, first or third
//     party). Both transports are available everywhere; the declaration wins.
//   - manifest.TrustClass: third_party forces subprocess — crash isolation
//     for untrusted code is a hard safety requirement, never an in-process
//     choice, and overrides an in-process declaration.
//   - host run mode (dev/prod flag): a dev host falls back to subprocess when
//     isolation is undeclared and the artifact on disk is a subprocess build.
//   - artifact type on disk: a c-shared library (.dll/.so/.dylib) is an
//     in-process artifact; anything else (e.g. .exe or an extension-less Unix
//     executable) is a subprocess artifact. Load already verified the file
//     exists; this is the existence/type check, and a mismatch between the
//     declared isolation and the artifact kind is a hard error.
//
// Decision priority:
//  1. TrustClass == third_party -> subprocess, forced. A shared-library
//     artifact here is an error: the safety requirement cannot be met.
//  2. abi.Isolation == subprocess -> subprocess, on any host. A shared-library
//     artifact here is an error: the declaration cannot be honored.
//  3. abi.Isolation == inprocess -> inprocess. An executable artifact here is
//     an error: the declaration cannot be honored.
//  4. host dev mode AND a subprocess artifact is present -> subprocess
//     (undeclared isolation dev fallback).
//  5. everything else -> inprocess.
func selectTransport(abi gen.PluginAbi, artifactPath string, devMode bool) (string, error) {
	switch {
	case abi.TrustClass == pluginhost.TrustThirdParty:
		if isSharedLibraryArtifact(artifactPath) {
			return "", fmt.Errorf("pluginhost: third-party plugin %q requires the subprocess transport (crash isolation) but artifact %q is a shared library", abi.Name, artifactPath)
		}
		return pluginhost.IsolationSubprocess, nil
	case abi.Isolation == pluginhost.IsolationSubprocess:
		if isSharedLibraryArtifact(artifactPath) {
			return "", fmt.Errorf("pluginhost: plugin %q declares subprocess isolation but artifact %q is a shared library", abi.Name, artifactPath)
		}
		return pluginhost.IsolationSubprocess, nil
	case abi.Isolation == pluginhost.IsolationInProcess:
		if !isSharedLibraryArtifact(artifactPath) {
			return "", fmt.Errorf("pluginhost: plugin %q declares in-process isolation but artifact %q is not a shared library", abi.Name, artifactPath)
		}
		return pluginhost.IsolationInProcess, nil
	case devMode && !isSharedLibraryArtifact(artifactPath):
		return pluginhost.IsolationSubprocess, nil
	default:
		// Undeclared isolation, non-dev host: default to in-process. That
		// transport can only open a c-shared library; an executable here means
		// the artifact was built for subprocess but isolation was not declared
		// and the host is not in dev mode — handing it to the FFI loader today
		// silently fails deep inside dlopen/LoadLibrary. Fail loudly instead.
		if !isSharedLibraryArtifact(artifactPath) {
			return "", fmt.Errorf("executable artifact requires dev mode or subprocess isolation: %q is not a shared library and the in-process transport can only open shared libraries", artifactPath)
		}
		return pluginhost.IsolationInProcess, nil
	}
}

// isSharedLibraryArtifact reports whether path points at a c-shared library
// (an in-process artifact) rather than a standalone executable (a subprocess
// artifact). The check is extension-based: .dll/.so/.dylib are shared
// libraries; .exe and extension-less Unix binaries are executables.
func isSharedLibraryArtifact(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".dll", ".so", ".dylib":
		return true
	}
	return false
}

// transportOpener is the single ArtifactOpener the actor installs via
// ArtifactLoader.SetOpener (pkg/pluginhost/artifact.go:119). It holds both
// concrete transports — the FFI loaderOpener (in-process) and the processOpener
// (subprocess) — and picks per load with selectTransport, so the loader core
// stays transport-agnostic. Selection logic lives here on the actor side.
type transportOpener struct {
	devMode    bool
	inprocess  pluginhost.ArtifactOpener // loaderOpener (FFI/purego)
	subprocess pluginhost.ArtifactOpener // processOpener (child process)
}

// Open implements pluginhost.ArtifactOpener: route the load to the opener
// selected by selectTransport for this manifest+artifact.
func (o *transportOpener) Open(abi gen.PluginAbi, artifactPath, entrySymbol, pluginID string, allowed map[string]struct{}, onLoadConfig []byte) (func(ctx context.Context, callable string, request []byte) ([]byte, error), pluginhost.InvokeStreamFunc, func() error, string, error) {
	transport, err := selectTransport(abi, artifactPath, o.devMode)
	if err != nil {
		return nil, nil, nil, "", err
	}
	if transport == pluginhost.IsolationSubprocess {
		// A fresh processOpener per load. The opener owns single-process
		// transport state (cmd/pipes plus the already-spawned guard), so
		// sharing one instance across plugins would cap the host at a
		// single live subprocess plugin — the second load fails with
		// "already spawned" (observed loading an app while another
		// subprocess app was running). Clone the config-carrying fields
		// from the prototype; transport state starts zeroed. Non-
		// processOpener subprocess implementations (test fakes) pass
		// through untouched.
		if proto, ok := o.subprocess.(*processOpener); ok {
			opener := &processOpener{
				dispatch:       proto.dispatch,
				dispatchStream: proto.dispatchStream,
				storeDispatch:  proto.storeDispatch,
				logger:         proto.logger,
				reverse:        proto.reverse,
				env:            proto.env,
				onLoadConfig:   proto.onLoadConfig,
				settleGrace:    proto.settleGrace,
				observeClone:   proto.observeClone,
			}
			if proto.observeClone != nil {
				proto.observeClone(opener)
			}
			return opener.Open(abi, artifactPath, entrySymbol, pluginID, allowed, onLoadConfig)
		}
		return o.subprocess.Open(abi, artifactPath, entrySymbol, pluginID, allowed, onLoadConfig)
	}
	return o.inprocess.Open(abi, artifactPath, entrySymbol, pluginID, allowed, onLoadConfig)
}

// newTransportOpener is the actor-side opener factory: it wires both
// transports from the same dispatch/storeDispatch wires (per-plugin
// capability gating identical across transports) plus the plugin log sink,
// and records whether the host build is a dev build (buildinfo.IsDev). The
// processOpener's ReverseHandler is left nil on purpose — the default
// capability-gated HostBridge built inside Open from these wires is the T6
// seam implementation (host_bridge.go), keeping per-plugin allowed sets
// correct at open time. streamDispatch is the streaming llm.* route
// installed on that default bridge; a nil value keeps reverse calls unary.
func newTransportOpener(dispatch HostDispatchFunc, streamDispatch HostDispatchStreamFunc, storeDispatch HostDispatchFunc, logger pluginLogSink, devMode bool) *transportOpener {
	return &transportOpener{
		devMode:    devMode,
		inprocess:  loaderOpener{dispatch: dispatch, storeDispatch: storeDispatch, logger: logger},
		subprocess: &processOpener{dispatch: dispatch, dispatchStream: streamDispatch, storeDispatch: storeDispatch, logger: logger},
	}
}
