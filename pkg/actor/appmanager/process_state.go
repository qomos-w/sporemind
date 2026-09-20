package appmanager

import (
	"fmt"
	"log/slog"
	"regexp"

	"github.com/qomos-w/gospore/actor"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// processStateCauseLimit caps the crash cause before it crosses an actor
// boundary (record.Error, app_lifecycle event, oracle diagnostic) — the
// respSnippet 512-byte truncation discipline.
const processStateCauseLimit = 512

// pathLeakPattern matches absolute filesystem paths embedded in OS error
// strings — Windows drive (`F:\dir\file.exe`), UNC (`\\server\share\...`),
// and common Unix roots (`/home/...`). The leading group is a non-word
// boundary: `p:/` inside `http://` must not count as a drive letter.
// Everything matched is replaced with "[path]" (keeping the boundary char)
// before a cause reaches record.Error / the app_lifecycle event / the
// frontend error panel: the app UI must not disclose the host's run
// directory layout (2026-09 audit item "plugin 前端泄漏运行目录").
var pathLeakPattern = regexp.MustCompile(`(^|[^\w.])(?:\\\\[^\s"':]+|[A-Za-z]:[\\/][^\s"':]+|/(?:Users|home|tmp|var|opt|root|mnt|data)/[^\s"':]+)`)

// redactProcessCause scrubs filesystem paths out of a process-state cause
// and truncates it to processStateCauseLimit. Every writer of record.Error
// and every app_lifecycle Message goes through this single choke point;
// the unredacted original stays in host-side logs.
func redactProcessCause(s string) string {
	s = pathLeakPattern.ReplaceAllString(s, "${1}[path]")
	if len(s) > processStateCauseLimit {
		s = s[:processStateCauseLimit] + "...(truncated)"
	}
	return s
}

// handleReportProcessState is the internal callable (actor-to-actor Invoke;
// not exported to any frontend surface) behind pluginhost's process-state
// lifecycle seam. pluginhost already classifies the verdict — abnormal exits
// arrive as "crashed" with a cause, explicit unloads as "stopped", restore /
// load failures as "failed" — so this handler only writes the record,
// persists, eventizes failures, and — on a backend switch — announces the
// bumped session generation so stale panels remount.
//
// Stateless (PureContext) by design: the record transition runs under a.mu
// (the actor's single logical writer across lanes), while Save (disk I/O)
// and the attach/detach/event/diagnostic tells execute on the caller's
// forked pure goroutine — never parking the owner lane behind persistence.
// No cross-actor Await.
func (a *Actor) handleReportProcessState(ctx actor.PureContext, req gen.AppManagerReportProcessStateReq) error {
	if req.PluginID == "" || req.State == "" {
		return nil
	}
	failed := req.State == stateCrashed || req.State == stateFailed
	var manifest gen.AppManifest
	var record appRecord
	known := false
	attach := false
	a.withMu(func() {
		var hasRecord bool
		record, hasRecord = a.Records[req.PluginID]
		if !hasRecord {
			return
		}
		known = true
		manifest = a.Apps[req.PluginID]
		record.State = req.State
		if failed {
			record.Error = redactProcessCause(req.CrashCause)
		} else {
			record.Error = ""
		}
		// A crashed/failed/stopped verdict means the process (and its HTTP
		// listener) is gone: drop the bootstrap URL and cookie secret so status
		// surfaces stop advertising a dead origin. A "running" report carrying
		// the fresh HttpAddr (cold-start restore, where the ephemeral port
		// changed) refreshes the record's backend and re-attaches the gateway
		// proxy with a token minted from the report's SessionSecret — the
		// secret the live process was ACTUALLY spawned with. Adopting it heals
		// the drift window where the appmanager record and pluginhost's
		// persisted ArtifactLoads were written with different secrets (crash
		// between the two actors' Saves): before this, the proxy minted tokens
		// from the stale record secret and every /plugin/{id} request 401'd
		// until a manual unload/load.
		// Exception: during a reload (reloadingApps guard) the candidate's
		// "running" report must NOT switch the proxy — the record still holds
		// the pre-reload secret while the candidate listener already runs the
		// fresh one, so an early switch 401s every request until commitBackend
		// installs the new secret atomically.
		if failed || req.State == stateStopped {
			clearBackend(&record)
		} else if req.State == stateRunning && req.HttpAddr != "" {
			adopted := req.SessionSecret != "" && req.SessionSecret != record.SessionSecret
			if !backendMatches(record, req.HttpAddr) || adopted {
				if a.reloadingApps[req.PluginID] {
					// Reload in flight: leave the proxy on the old backend;
					// commitBackend switches it after the reload commits.
					slog.Debug("appmanager: reload in flight; deferring proxy switch for running report",
						"plugin", req.PluginID, "addr", req.HttpAddr)
				} else {
					secret := record.SessionSecret
					if req.SessionSecret != "" {
						secret = req.SessionSecret
					}
					setBackendFromLoad(&record, secret, req.HttpAddr)
					// New process session: bump the generation with the
					// backend switch so mounted panels remount (see below).
					record.Generation++
					attach = true
				}
			}
		}
		a.Records[req.PluginID] = record
	})
	if !known {
		// pluginhost can outlive the appmanager record (orphaned artifact or
		// a raced unregister); the report is best-effort telemetry.
		slog.Debug("appmanager: report_process_state for unknown plugin", "plugin", req.PluginID, "state", req.State)
		return nil
	}
	if err := a.Save(); err != nil {
		return fmt.Errorf("appmanager: persist process state for %q: %w", req.PluginID, err)
	}
	if attach {
		a.attachBackendProxy(ctx, req.PluginID, record)
		// Remount signal for restored panels: after a host restart the
		// frontend can mount a panel while /plugin/{id} still serves the
		// static assets handler — a document whose data-plane routes 404
		// until this attach. The generation bump carried by this event makes
		// every mounted iframe remount through the live proxy (PluginIframe
		// keys on generation), healing the panel without a manual refresh
		// (2026-09-17 restored-panel report).
		a.emitLifecycleEvent(ctx, gen.AppLifecycleEvent{
			Kind: "reloaded", ID: req.PluginID, Runtime: manifest.Runtime,
			State: stateRunning, Version: manifest.Version, Generation: record.Generation,
		})
	}
	if failed || req.State == stateStopped {
		// The listener is gone — drop the gateway proxy route too. The
		// pluginhost already self-detaches on its crash/stop report; this
		// tell keeps appmanager's clear authoritative and is idempotent.
		a.detachBackendProxy(ctx, req.PluginID)
	}

	if failed {
		cause := redactProcessCause(req.CrashCause)
		a.emitLifecycleEvent(ctx, gen.AppLifecycleEvent{
			Kind: req.State, ID: req.PluginID, Runtime: manifest.Runtime,
			State: req.State, Version: manifest.Version, Error: cause,
		})
		a.auditLifecycle(req.PluginID, "report_process_state", req.State+": "+cause)
		a.reportPluginProblem(ctx, req.PluginID, req.State, cause)
	}
	return nil
}

// handlePluginhostOnline is the deterministic cold-start proxy reconciliation.
// pluginhost fires appmanager.pluginhost_online at the end of its OnStart,
// after its own domain is exposed. By that point the restore-time "running"
// reports have (or will) refresh the records' backend state, and — unlike when
// those reports were handled — pluginhost is now resolvable, so the attach
// tells below succeed. This closes the gap where a cold-start running report
// was processed while pluginhost was still unexposed: its attach tell was
// dropped and never retried, leaving /plugin/{id}/invoke|events 405/404 until
// an external reload.
//
// Every re-attach bumps the record's session generation and emits a
// "reloaded" lifecycle event carrying it: at cold start the events fired by
// the restore-time reports are structurally lost (the gateway — and with it
// any frontend subscriber — only comes up after every OnStart returns), so
// this is the first attach announcement the frontend can actually observe.
// Mounted panels keyed on generation remount through the live proxy instead
// of keeping whatever document they loaded while the static assets handler
// still owned the route.
//
// Re-attaching is idempotent (pluginhost router replace semantics). Reports
// still in flight land after this and re-attach with their fresh addr the
// same way handleReportProcessState always has, so any interleaving converges.
// A reload in flight keeps its deferral: commitBackend attaches on commit.
//
// Stateless (PureContext): the map scan runs under a.mu and the attach tells
// are fire-and-forget, so the reconciliation runs on a forked pure goroutine
// without parking the owner lane. No cross-actor Await.
func (a *Actor) handlePluginhostOnline(ctx actor.PureContext, _ gen.AppManagerPluginhostOnlineReq) error {
	type attachTarget struct {
		id      string
		record  appRecord
	}
	var targets []attachTarget
	a.withMu(func() {
		for appID, record := range a.Records {
			manifest, hasApp := a.Apps[appID]
			if !hasApp || manifest.Runtime != "native" {
				continue
			}
			if record.State != stateRunning || record.BackendUrl == "" {
				continue
			}
			if a.reloadingApps[appID] {
				// Reload in flight: commitBackend switches the proxy after
				// the reload commits (same deferral as report handling).
				continue
			}
			// Bump per re-attached app (not once globally) so each panel
			// gets its own remount signal.
			record.Generation++
			a.Records[appID] = record
			targets = append(targets, attachTarget{id: appID, record: record})
		}
	})
	if len(targets) > 0 {
		if err := a.Save(); err != nil {
			return fmt.Errorf("appmanager: persist pluginhost online reconciliation: %w", err)
		}
	}
	for _, t := range targets {
		a.attachBackendProxy(ctx, t.id, t.record)
		a.emitLifecycleEvent(ctx, gen.AppLifecycleEvent{
			Kind: "reloaded", ID: t.id, Runtime: t.record.Manifest.Runtime,
			State: stateRunning, Version: t.record.Manifest.Version, Generation: t.record.Generation,
		})
	}
	if len(targets) > 0 {
		slog.Info("appmanager: pluginhost online; reconciled gateway proxy routes", "apps", len(targets))
	}
	return nil
}

// reportPluginProblem records a failed/crashed plugin as an oracle diagnostic
// so it surfaces in get_problems. Mirrors aiaggregator.reportOracleDiagnostic:
// fire-and-forget Tell, payload pre-truncated at the source, resolve/send
// failures logged and never retried.
func (a *Actor) reportPluginProblem(ctx actor.PureContext, pluginID, state, cause string) {
	oracleRef, ok := ctx.LookupService("oracle")
	if !ok || oracleRef == nil {
		slog.Debug("appmanager: oracle service not available; plugin problem not reported", "plugin", pluginID, "state", state)
		return
	}
	req := domain.OracleReportDiagnosticReq{
		Severity: "error",
		Source:   "appmanager",
		Message:  redactProcessCause("native plugin " + state + ": " + pluginID + ": " + cause),
		RawData:  cause,
	}
	call := oracleRef.Invoke(ctx.Lifecycle(), "oracle.report_diagnostic", req)
	_ = call.Close()
}
