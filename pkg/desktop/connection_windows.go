package desktop

import (
	"fmt"

	"github.com/qomos-w/sporemind/pkg/instanceid"
)

// emitActiveConnection broadcasts the newly active client target to every
// frame as the "connections:active" Wails custom event. It is a package-level
// seam (mirrors openDirectoryInFileManager) so tests can observe the broadcast
// without standing up a live Wails application.
var emitActiveConnection = func(a *App, target string) {
	if a == nil || a.app == nil {
		return
	}
	a.app.Event.Emit("connections:active", map[string]string{"target": target})
}

// LocalTarget names the local client in SwitchConnection.
const LocalTarget = "local"

// ActiveConnection describes the currently active client target.
type ActiveConnection struct {
	Target string                `json:"target"` // "local" or connection id
	Local  bool                  `json:"local"`
	Name   string                `json:"name"`
	Conn   *RemoteConnectionView `json:"conn,omitempty"`
}

// GetActiveConnection reports which client target the main window currently
// has active.
func (a *App) GetActiveConnection() ActiveConnection {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	return a.activeConnectionLocked()
}

func (a *App) activeConnectionLocked() ActiveConnection {
	if a.activeTarget == "" || a.activeTarget == LocalTarget {
		return ActiveConnection{Target: LocalTarget, Local: true, Name: "local"}
	}
	if doc, err := a.connectionsLocked(); err == nil {
		for _, c := range doc.Connections {
			if c.ID == a.activeTarget {
				v := c.view()
				return ActiveConnection{Target: c.ID, Name: c.Name, Conn: &v}
			}
		}
	}
	return ActiveConnection{Target: LocalTarget, Local: true, Name: "local"}
}

// SwitchConnection records the requested client target as active and
// broadcasts the change; it does not navigate or reload the window.
//
// Single-window model: the shell (theme, switcher) stays resident. A switch is
// a pure state change — the frontend reacts to the "connections:active" Wails
// event by rebinding its gateway client and re-mounting the instance subtree
// with the new instanceId (see instance-swap-refactor). The previous client's
// in-page state is discarded by that remount, not by a navigation. activeTarget
// lives in the host process so the switcher UI stays truthful.
//
// Remote targets are probed first: unreachable gateways are refused, and a
// fingerprint equal to this installation's own (a client pointed at itself)
// is a hard error.
func (a *App) SwitchConnection(target string) error {
	if a.app == nil || a.window == nil {
		return fmt.Errorf("desktop: not started")
	}
	if target == "" {
		target = LocalTarget
	}

	a.connMu.Lock()
	current := a.activeTarget
	a.connMu.Unlock()
	if current == target {
		return nil // already active; re-broadcasting would restart the instance
	}

	if target == LocalTarget {
		a.connMu.Lock()
		a.activeTarget = LocalTarget
		a.connMu.Unlock()
		emitActiveConnection(a, LocalTarget)
		return nil
	}

	var conn remoteConnection
	a.connMu.Lock()
	doc, err := a.connectionsLocked()
	if err != nil {
		a.connMu.Unlock()
		return err
	}
	found := false
	for _, c := range doc.Connections {
		if c.ID == target {
			conn, found = c, true
			break
		}
	}
	if !found {
		a.connMu.Unlock()
		return fmt.Errorf("desktop: connection %q not found", target)
	}
	a.connMu.Unlock()

	probe := probeInstance(connectionBaseURL(conn.Host, conn.Port), instanceid.Get())
	if !probe.Reachable {
		return fmt.Errorf("desktop: cannot reach %s:%d — %s", conn.Host, conn.Port, probe.Message)
	}
	if probe.IsSelf {
		return fmt.Errorf("desktop: %s:%d is this same instance — remote clients cannot connect to themselves", conn.Host, conn.Port)
	}

	// Remember the observed fingerprint for the switcher UI.
	if probe.InstanceID != "" && probe.InstanceID != conn.InstanceID {
		a.connMu.Lock()
		for i := range doc.Connections {
			if doc.Connections[i].ID == target {
				doc.Connections[i].InstanceID = probe.InstanceID
				_ = a.saveConnectionsLocked(doc)
			}
		}
		a.connMu.Unlock()
	}

	a.connMu.Lock()
	a.activeTarget = target
	a.connMu.Unlock()

	emitActiveConnection(a, target)
	return nil
}
