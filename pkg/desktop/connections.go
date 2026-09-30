package desktop

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/instanceid"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// DefaultRemotePort is the sporemind gateway port assumed when a saved
// connection does not specify one.
const DefaultRemotePort = 18080

// connectionsStoreKey is the persist name under the desktop namespace.
const connectionsStoreKey = "connections"

// probeTimeout bounds the pre-connect reachability/fingerprint check.
const probeTimeout = 3 * time.Second

// remoteAuthTimeout bounds the host-side login proxy call.
const remoteAuthTimeout = 10 * time.Second

// RemoteConnectionView is the frontend-facing shape of a saved remote
// connection. It never carries the password (encrypted or plain).
// UpdatedAt is RFC3339 string on the wire: a time.Time field in a bound
// argument struct makes the Wails layer time.Parse every inbound call, and
// an empty/zero value from the frontend then fails before the handler runs.
type RemoteConnectionView struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	Username     string `json:"username"`
	InstanceID   string `json:"instanceId,omitempty"`
	InstanceName string `json:"instanceName,omitempty"`
	UpdatedAt    string `json:"updatedAt"`
	HasPassword  bool   `json:"hasPassword"`
}

// remoteConnection is the persisted record. PasswordEnc is AES-GCM sealed
// (nonce || ciphertext, base64) with the per-installation connections key.
type remoteConnection struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Host        string    `json:"host"`
	Port        int       `json:"port"`
	Username    string    `json:"username"`
	PasswordEnc string    `json:"passwordEnc,omitempty"`
	InstanceID  string    `json:"instanceId,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func (c remoteConnection) view() RemoteConnectionView {
	updatedAt := ""
	if !c.UpdatedAt.IsZero() {
		updatedAt = c.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return RemoteConnectionView{
		ID:          c.ID,
		Name:        c.Name,
		Host:        c.Host,
		Port:        c.Port,
		Username:    c.Username,
		InstanceID:  c.InstanceID,
		UpdatedAt:   updatedAt,
		HasPassword: c.PasswordEnc != "",
	}
}

// connectionsDoc is the persisted document.
type connectionsDoc struct {
	Connections []remoteConnection `json:"connections"`
}

// ProbeResult reports a pre-connect probe of a remote gateway.
type ProbeResult struct {
	Reachable  bool   `json:"reachable"`
	Message    string `json:"message,omitempty"`
	InstanceID string `json:"instanceId,omitempty"`
	Version    string `json:"version,omitempty"`
	IsSelf     bool   `json:"isSelf"`
}

// ---------------------------------------------------------------------------
// Password sealing (AES-GCM, per-installation key file)
// ---------------------------------------------------------------------------

var (
	connKeyOnce sync.Once
	connKeyErr  error
	connKey     []byte
)

func connectionKeyPath() string {
	return filepath.Join(config.DataDir(), "connections-key")
}

// loadConnectionKey returns the 32-byte AES key for sealing saved passwords,
// generating and persisting it on first use (0600, next to jwt-secret).
func loadConnectionKey() ([]byte, error) {
	connKeyOnce.Do(func() {
		path := connectionKeyPath()
		if raw, err := os.ReadFile(path); err == nil && len(raw) == 32 {
			connKey = raw
			return
		}
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			connKeyErr = fmt.Errorf("desktop: generate connections key: %w", err)
			return
		}
		if err := os.MkdirAll(config.DataDir(), 0700); err != nil {
			connKeyErr = fmt.Errorf("desktop: connections key dir: %w", err)
			return
		}
		if err := os.WriteFile(path, key, 0600); err != nil {
			connKeyErr = fmt.Errorf("desktop: persist connections key: %w", err)
			return
		}
		connKey = key
	})
	return connKey, connKeyErr
}

func sealPassword(key []byte, plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func openPassword(key []byte, enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("desktop: sealed password too short")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("desktop: unseal password: %w", err)
	}
	return string(plain), nil
}

// ---------------------------------------------------------------------------
// Host helpers
// ---------------------------------------------------------------------------

// sanitizeHost strips accidental scheme/path decoration from a user-entered
// host so probe/login always target "http://host:port/...".
func sanitizeHost(in string) string {
	h := strings.TrimSpace(in)
	h = strings.TrimPrefix(h, "ws://")
	h = strings.TrimPrefix(h, "wss://")
	h = strings.TrimPrefix(h, "http://")
	h = strings.TrimPrefix(h, "https://")
	if i := strings.IndexAny(h, "/?#"); i >= 0 {
		h = h[:i]
	}
	return strings.TrimSpace(h)
}

// connectionHostPort joins host and port for a URL authority, bracketing a
// bare IPv6 literal.
func connectionHostPort(host string, port int) string {
	h := sanitizeHost(host)
	if strings.Contains(h, ":") && !strings.HasPrefix(h, "[") {
		if ip := net.ParseIP(h); ip != nil && ip.To4() == nil {
			h = "[" + h + "]"
		}
	}
	return h + ":" + strconv.Itoa(port)
}

// connectionBaseURL builds "http://host:port" for a saved connection shape.
func connectionBaseURL(host string, port int) string {
	return "http://" + connectionHostPort(host, port)
}

// ---------------------------------------------------------------------------
// Pure probe / login (testable without a live App)
// ---------------------------------------------------------------------------

// probeInstance probes baseURL/instance/info and compares the reported
// fingerprint with localID.
func probeInstance(baseURL, localID string) ProbeResult {
	client := &http.Client{Timeout: probeTimeout}
	resp, err := client.Get(baseURL + "/instance/info")
	if err != nil {
		return ProbeResult{Reachable: false, Message: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Any HTTP response means a server is listening. A 404 here is an
		// older sporemind without the fingerprint endpoint (or a non-gateway
		// service — login will surface that failure instead).
		return ProbeResult{Reachable: true, Message: "no /instance/info (HTTP " + resp.Status + ") — remote version too old or not a sporemind gateway"}
	}
	var info struct {
		InstanceID string `json:"instanceId"`
		Version    string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&info); err != nil {
		return ProbeResult{Reachable: false, Message: "invalid response: " + err.Error()}
	}
	if info.InstanceID == "" {
		// Reachable but no fingerprint endpoint payload: an older sporemind.
		return ProbeResult{Reachable: true, Message: "remote reports no instance fingerprint (older version)"}
	}
	return ProbeResult{
		Reachable:  true,
		InstanceID: info.InstanceID,
		Version:    info.Version,
		IsSelf:     info.InstanceID == localID,
	}
}

// RemoteAuthResult mirrors the remote user.auth_login response so the saved
// credentials never need to reach the frontend.
type RemoteAuthResult struct {
	Token        string         `json:"Token"`
	RefreshToken string         `json:"RefreshToken"`
	ExpiresAt    string         `json:"ExpiresAt"`
	Account      map[string]any `json:"Account,omitempty"`
}

// remoteAuthLogin POSTs credentials to baseURL/api/user.auth_login and
// returns the parsed response on success.
func remoteAuthLogin(baseURL, username, password string) (RemoteAuthResult, error) {
	body, err := json.Marshal(map[string]string{"Username": username, "Password": password})
	if err != nil {
		return RemoteAuthResult{}, err
	}
	client := &http.Client{Timeout: remoteAuthTimeout}
	resp, err := client.Post(baseURL+"/api/user.auth_login", "application/json", bytes.NewReader(body))
	if err != nil {
		return RemoteAuthResult{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return RemoteAuthResult{}, err
	}
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return RemoteAuthResult{}, fmt.Errorf("remote login failed (HTTP %d): %s", resp.StatusCode, msg)
	}
	var out RemoteAuthResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return RemoteAuthResult{}, fmt.Errorf("desktop: decode remote login response: %w", err)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// App-bound Wails methods
// ---------------------------------------------------------------------------

// connectionsLocked loads (once) and returns the connections doc. Callers
// must hold a.connMu.
func (a *App) connectionsLocked() (*connectionsDoc, error) {
	if a.conns != nil {
		return a.conns, nil
	}
	doc := &connectionsDoc{}
	if err := persist.LoadOrZero(store, connectionsStoreKey, doc); err != nil {
		return nil, err
	}
	a.conns = doc
	return doc, nil
}

func (a *App) saveConnectionsLocked(doc *connectionsDoc) error {
	if err := store.Save(connectionsStoreKey, doc); err != nil {
		return err
	}
	a.conns = doc
	return nil
}

// ConnectionsList returns the saved remote connections (without passwords).
func (a *App) ConnectionsList() ([]RemoteConnectionView, error) {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	doc, err := a.connectionsLocked()
	if err != nil {
		return nil, err
	}
	views := make([]RemoteConnectionView, 0, len(doc.Connections))
	for _, c := range doc.Connections {
		views = append(views, c.view())
	}
	return views, nil
}

// ConnectionsSave creates or updates a connection. An empty password keeps
// the previously saved one.
func (a *App) ConnectionsSave(conn RemoteConnectionView, password string) (RemoteConnectionView, error) {
	conn.Host = sanitizeHost(conn.Host)
	if conn.Host == "" {
		return RemoteConnectionView{}, fmt.Errorf("desktop: host is required")
	}
	if conn.Name == "" {
		conn.Name = conn.Host
	}
	if conn.Port <= 0 {
		conn.Port = DefaultRemotePort
	}
	if conn.ID == "" {
		raw := make([]byte, 8)
		if _, err := rand.Read(raw); err != nil {
			return RemoteConnectionView{}, err
		}
		conn.ID = hex.EncodeToString(raw)
	}
	key, err := loadConnectionKey()
	if err != nil {
		return RemoteConnectionView{}, err
	}

	a.connMu.Lock()
	defer a.connMu.Unlock()
	doc, err := a.connectionsLocked()
	if err != nil {
		return RemoteConnectionView{}, err
	}
	record := remoteConnection{
		ID:         conn.ID,
		Name:       conn.Name,
		Host:       conn.Host,
		Port:       conn.Port,
		Username:   conn.Username,
		InstanceID: conn.InstanceID,
		UpdatedAt:  time.Now().UTC(),
	}
	// Reject saving a target already known to be this very instance.
	if record.InstanceID != "" && record.InstanceID == instanceid.Get() {
		return RemoteConnectionView{}, fmt.Errorf("desktop: cannot save a connection to this same instance")
	}
	for i, existing := range doc.Connections {
		if existing.ID == record.ID {
			if password == "" {
				record.PasswordEnc = existing.PasswordEnc
			}
			if record.InstanceID == "" {
				record.InstanceID = existing.InstanceID
			}
			doc.Connections[i] = record
			if err := a.saveConnectionsLocked(doc); err != nil {
				return RemoteConnectionView{}, err
			}
			return record.view(), nil
		}
	}
	record.PasswordEnc, err = sealPassword(key, password)
	if err != nil {
		return RemoteConnectionView{}, err
	}
	doc.Connections = append(doc.Connections, record)
	if err := a.saveConnectionsLocked(doc); err != nil {
		return RemoteConnectionView{}, err
	}
	return record.view(), nil
}

// ConnectionsDelete removes a saved connection. If it was the active target,
// the window falls back to the local client.
func (a *App) ConnectionsDelete(id string) error {
	a.connMu.Lock()
	doc, err := a.connectionsLocked()
	if err != nil {
		a.connMu.Unlock()
		return err
	}
	kept := doc.Connections[:0]
	found := false
	for _, c := range doc.Connections {
		if c.ID == id {
			found = true
			continue
		}
		kept = append(kept, c)
	}
	if !found {
		a.connMu.Unlock()
		return fmt.Errorf("desktop: connection %q not found", id)
	}
	doc.Connections = kept
	if err := a.saveConnectionsLocked(doc); err != nil {
		a.connMu.Unlock()
		return err
	}
	wasActive := a.activeTarget == id
	a.connMu.Unlock()

	if wasActive {
		return a.SwitchConnection(LocalTarget)
	}
	return nil
}

// ConnectionsProbe checks a remote gateway for reachability and self-identity
// before it may be saved or connected to.
func (a *App) ConnectionsProbe(host string, port int) ProbeResult {
	if port <= 0 {
		port = DefaultRemotePort
	}
	return probeInstance(connectionBaseURL(host, port), instanceid.Get())
}

// RemoteAuthLogin performs a saved-credential login against the connection's
// remote gateway from the host process, returning the auth response. The
// password never crosses into the frontend.
func (a *App) RemoteAuthLogin(connID string) (RemoteAuthResult, error) {
	a.connMu.Lock()
	doc, err := a.connectionsLocked()
	if err != nil {
		a.connMu.Unlock()
		return RemoteAuthResult{}, err
	}
	var conn *remoteConnection
	for i := range doc.Connections {
		if doc.Connections[i].ID == connID {
			conn = &doc.Connections[i]
			break
		}
	}
	if conn == nil {
		a.connMu.Unlock()
		return RemoteAuthResult{}, fmt.Errorf("desktop: connection %q not found", connID)
	}
	if conn.PasswordEnc == "" || conn.Username == "" {
		a.connMu.Unlock()
		return RemoteAuthResult{}, fmt.Errorf("desktop: connection has no saved credentials")
	}
	key, err := loadConnectionKey()
	if err != nil {
		a.connMu.Unlock()
		return RemoteAuthResult{}, err
	}
	password, err := openPassword(key, conn.PasswordEnc)
	if err != nil {
		a.connMu.Unlock()
		return RemoteAuthResult{}, err
	}
	baseURL := connectionBaseURL(conn.Host, conn.Port)
	username := conn.Username
	a.connMu.Unlock()
	return remoteAuthLogin(baseURL, username, password)
}

// GetInstanceFingerprint exposes this installation's stable instance id so
// the frontend can label the local target and double-check self-connections.
func (a *App) GetInstanceFingerprint() string {
	return instanceid.Get()
}
