package sdk

import (
	"encoding/json"
	"fmt"
	"sync"
)

// Plugin is the top-level plugin definition registered with the SDK.
type Plugin struct {
	Manifest       Manifest
	OnLoad         func(ctx Context) error
	OnUnload       func(ctx Context) error
	OnConfigChange func(ctx Context, config json.RawMessage) error
}

type Context interface {
	PluginID() string
	Manifest() Manifest
	Host() Host
	RegisterCallable(method string, h Handler)
	RegisterCallableStream(method string, h HandlerStream)
	UnregisterCallable(method string)
	// RegisterEventListener subscribes a host event listener (appdef `listen`
	// blocks). Delivery arrives over the reserved `__event__:<kind>` dispatch
	// name — see events.go.
	RegisterEventListener(kind string, h EventHandler)
	// EmitEvent publishes one app-declared event (appdef `event` blocks) to
	// the host bus; see the package-level EmitEvent. OnLoad closures may use
	// this form; handlers use the package-level sdk.EmitEvent (no Context in
	// scope there).
	EmitEvent(kind string, payload any) error
	// Log writes a structured log entry into the plugin's ring buffer.
	// The host drains this buffer through the PluginLog ABI after each
	// invoke, converting entries into structured host-side log output
	// with appID and generation context.
	Log(level int, format string, args ...interface{})
}

type pluginContext struct {
	pluginID string
	manifest Manifest
	host     Host
	logRing  *logRing
}

func (c *pluginContext) PluginID() string   { return c.pluginID }
func (c *pluginContext) Manifest() Manifest { return c.manifest }
func (c *pluginContext) Host() Host         { return c.host }
func (c *pluginContext) RegisterCallable(method string, h Handler) {
	registerCallable(c.pluginID, method, h)
}
func (c *pluginContext) RegisterCallableStream(method string, h HandlerStream) {
	registerCallableStream(c.pluginID, method, h)
}
func (c *pluginContext) UnregisterCallable(method string) {
	unregisterCallable(c.pluginID, method)
}
func (c *pluginContext) RegisterEventListener(kind string, h EventHandler) {
	registerEventListener(c.pluginID, kind, h)
}
func (c *pluginContext) EmitEvent(kind string, payload any) error {
	return EmitEvent(kind, payload)
}
func (c *pluginContext) Log(level int, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	// Subprocess transport: emit a 0x05 log frame directly (decision D1 on
	// the T4 card) instead of buffering in the ring. The ring is only for
	// the c-shared PluginLog drain handshake.
	if w := processLogWriter(); w != nil {
		_ = writeLogFrame(w, level, msg)
		return
	}
	if c.logRing == nil {
		return
	}
	c.logRing.push(level, msg)
}

type lifecycleState struct {
	mu        sync.RWMutex
	pluginID  string
	manifest  Manifest
	plugin    *Plugin
	ctx       Context
	host      Host
	logRing   *logRing
	loaded    bool
	unloading bool
}

var activeState = struct {
	sync.RWMutex
	state *lifecycleState
}{}

var registeredPlugin = struct {
	sync.RWMutex
	plugin *Plugin
}{}

// Register registers the plugin definition before lifecycle callbacks begin.
func Register(p *Plugin) {
	registeredPlugin.Lock()
	defer registeredPlugin.Unlock()
	registeredPlugin.plugin = p
}

// ActivePlugin returns the currently registered plugin definition.
func ActivePlugin() *Plugin {
	registeredPlugin.RLock()
	defer registeredPlugin.RUnlock()
	return registeredPlugin.plugin
}

func registered() *Plugin {
	registeredPlugin.RLock()
	defer registeredPlugin.RUnlock()
	return registeredPlugin.plugin
}

func newContext(pluginID string, manifest Manifest, host Host, logRing *logRing) Context {
	return &pluginContext{pluginID: pluginID, manifest: manifest, host: host, logRing: logRing}
}

func ensureState(pluginID string) (*lifecycleState, error) {
	activeState.RLock()
	state := activeState.state
	activeState.RUnlock()
	if state != nil {
		state.mu.RLock()
		same := state.pluginID == pluginID && state.loaded && !state.unloading
		state.mu.RUnlock()
		if same {
			return state, nil
		}
	}
	plugin := registered()
	if plugin == nil {
		return nil, fmt.Errorf("plugin is not registered")
	}
	state = &lifecycleState{
		pluginID: pluginID,
		manifest: plugin.Manifest,
		plugin:   plugin,
		host:     activeHost(),
		logRing:  newLogRing(),
	}
	state.ctx = newContext(pluginID, state.manifest, state.host, state.logRing)
	activeState.Lock()
	activeState.state = state
	activeState.Unlock()
	return state, nil
}

func currentState() (*lifecycleState, error) {
	activeState.RLock()
	state := activeState.state
	activeState.RUnlock()
	if state == nil {
		return nil, fmt.Errorf("plugin is not loaded")
	}
	state.mu.RLock()
	loaded := state.loaded && !state.unloading
	state.mu.RUnlock()
	if !loaded {
		return nil, fmt.Errorf("plugin is not active")
	}
	return state, nil
}

// State returns the active plugin state for diagnostics.
func State() *pluginState {
	state, err := currentState()
	if err != nil {
		return nil
	}
	return &pluginState{pluginID: state.pluginID, host: state.host, ctx: state.ctx}
}

type pluginState struct {
	pluginID string
	host     Host
	ctx      Context
}
