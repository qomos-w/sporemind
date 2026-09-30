package desktop

import (
	"fmt"
	"net/url"
	goruntime "runtime"

	"github.com/qomos-w/sporemind/pkg/instanceid"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// LocalTarget names the main window's client in SwitchConnection.
const LocalTarget = "local"

// ActiveConnection describes the currently visible client target.
type ActiveConnection struct {
	Target string                `json:"target"` // "local" or connection id
	Local  bool                  `json:"local"`
	Name   string                `json:"name"`
	Conn   *RemoteConnectionView `json:"conn,omitempty"`
}

// GetActiveConnection reports which client target is currently visible.
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

// SwitchConnection brings a client target to the front. target "local" shows
// the main window; a connection id shows its dedicated window, creating it on
// first use. The previously visible window is only hidden, never destroyed —
// both clients keep their state and subscriptions running.
//
// Remote targets are probed first: unreachable gateways are refused, and a
// fingerprint equal to this installation's own (a client pointed at itself)
// is a hard error.
func (a *App) SwitchConnection(target string) error {
	if a.app == nil || a.window == nil {
		return fmt.Errorf("desktop: not started")
	}
	if target == "" || target == LocalTarget {
		a.connMu.Lock()
		a.activeTarget = LocalTarget
		windows := a.allConnectionWindowsLocked()
		a.connMu.Unlock()
		for _, w := range windows {
			w.Hide()
		}
		a.window.Show()
		a.window.Restore()
		a.window.Focus()
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
				conn.InstanceID = probe.InstanceID
			}
		}
		a.connMu.Unlock()
	}

	a.connMu.Lock()
	w := a.connWindows[target]
	a.activeTarget = target
	others := a.allConnectionWindowsLocked()
	delete(others, target)
	a.connMu.Unlock()

	if w == nil {
		var err error
		w, err = a.createConnectionWindow(conn)
		if err != nil {
			a.connMu.Lock()
			a.activeTarget = LocalTarget
			a.connMu.Unlock()
			return err
		}
		a.connMu.Lock()
		if a.connWindows == nil {
			a.connWindows = map[string]*application.WebviewWindow{}
		}
		a.connWindows[target] = w
		a.connMu.Unlock()
	}

	// Hide the main window and every other connection window; show and focus
	// the target. Hidden windows keep running — state stays isolated per
	// window and both clients stay live.
	a.window.Hide()
	for _, ow := range others {
		ow.Hide()
	}
	w.Show()
	w.Restore()
	w.Focus()
	return nil
}

// createConnectionWindow builds the dedicated window for a saved connection.
// It loads the same embedded frontend with ?server= pointing at the remote
// gateway and ?conn= identifying the profile, which forces the WS transport
// and the remote auto-login path in the frontend.
func (a *App) createConnectionWindow(conn remoteConnection) (*application.WebviewWindow, error) {
	mode, _ := parseThemePreference(mustReadBootThemeCache())
	query := url.Values{}
	query.Set("server", fmt.Sprintf("ws://%s:%d/ws", sanitizeHost(conn.Host), conn.Port))
	query.Set("conn", conn.ID)
	opts := application.WebviewWindowOptions{
		Name:            "conn-" + conn.ID,
		Title:           "sporemind — " + conn.Name,
		Width:           1280,
		Height:          800,
		EnableFileDrop:  true,
		Frameless:       goruntime.GOOS != "darwin",
		BackgroundType:  application.BackgroundTypeSolid,
		BackgroundColour: ShellBackgroundColour(mode == "dark"),
		URL:             "/?" + query.Encode(),
	}
	if goruntime.GOOS == "darwin" {
		opts.Mac = application.MacWindow{TitleBar: application.MacTitleBarHidden}
	}
	w := a.app.Window.NewWithOptions(opts)
	w.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
		a.connMu.Lock()
		wasActive := a.activeTarget == conn.ID
		delete(a.connWindows, conn.ID)
		if wasActive {
			a.activeTarget = LocalTarget
		}
		a.connMu.Unlock()
		if wasActive {
			// The visible client window went away — fall back to local.
			a.SwitchConnection(LocalTarget)
		}
	})
	return w, nil
}

// closeConnectionWindow tears down a connection's window; if it was visible,
// control returns to the local main window.
func (a *App) closeConnectionWindow(id string, switchToLocal bool) {
	a.connMu.Lock()
	w := a.connWindows[id]
	wasActive := a.activeTarget == id
	delete(a.connWindows, id)
	if wasActive {
		a.activeTarget = LocalTarget
	}
	a.connMu.Unlock()
	if w != nil {
		w.Close()
	}
	if wasActive && switchToLocal {
		_ = a.SwitchConnection(LocalTarget)
	}
}

func (a *App) allConnectionWindowsLocked() map[string]*application.WebviewWindow {
	out := make(map[string]*application.WebviewWindow, len(a.connWindows))
	for k, v := range a.connWindows {
		out[k] = v
	}
	return out
}

func mustReadBootThemeCache() string {
	if s, ok := readBootThemeCache(); ok {
		return s
	}
	return ""
}
