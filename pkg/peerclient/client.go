// Package peerclient is a client for connecting to a remote sporemind app's
// peer HTTP surface. It performs handshake, imports the remote exposed service
// surface into a local ProtocolManager, and invokes exposed callables.
package peerclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/qomos-w/sporemind/pkg/peerserver"
	"github.com/qomos-w/sporemind/pkg/protocol"
)

// AuthProvider attaches authentication credentials to an outgoing HTTP request.
// Implementations may set headers, query params, or sign the request.
type AuthProvider func(r *http.Request)

// TokenAuth returns an AuthProvider that sends the token in the standard
// X-Peer-Token header.
func TokenAuth(token string) AuthProvider {
	return func(r *http.Request) {
		r.Header.Set(peerserver.HeaderPeerToken, token)
	}
}

// Client connects to a remote sporemind peer server.
type Client struct {
	ServerURL    string
	ClientID     string
	LocalVersion string
	Auth         AuthProvider
	ProtoMgr     *protocol.Manager
	http         *http.Client
}

// New creates a peer client. ProtoMgr is the local protocol manager that will
// receive the remote peer's imported surface.
func New(serverURL, clientID, localVersion string, auth AuthProvider, protoMgr *protocol.Manager) *Client {
	if auth == nil {
		auth = func(r *http.Request) {}
	}
	return &Client{
		ServerURL:    serverURL,
		ClientID:     clientID,
		LocalVersion: localVersion,
		Auth:         auth,
		ProtoMgr:     protoMgr,
		http:         &http.Client{},
	}
}

// Handshake calls the remote /peer/handshake endpoint, registers the server as
// a peer, and imports its exposed service surface.
func (c *Client) Handshake(ctx context.Context) error {
	reqBody, err := json.Marshal(peerserver.HandshakeReq{
		ClientID: c.ClientID,
		Version:  c.LocalVersion,
	})
	if err != nil {
		return fmt.Errorf("marshal handshake: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.ServerURL+"/peer/handshake", bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.Auth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("handshake request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read handshake body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("handshake failed: %s", string(body))
	}

	var hs peerserver.HandshakeResp
	if err := json.Unmarshal(body, &hs); err != nil {
		return fmt.Errorf("decode handshake: %w", err)
	}

	// Register the server as a sporemind peer. If versions match, system will be
	// shared; otherwise it is treated as a generic external peer.
	if err := c.ProtoMgr.RegisterSporemindPeer(hs.ServerID, c.LocalVersion); err != nil {
		return fmt.Errorf("register server peer: %w", err)
	}
	if err := c.ProtoMgr.ImportPeerSurface(hs.ServerID, hs.Surface); err != nil {
		return fmt.Errorf("import peer surface: %w", err)
	}
	return nil
}

// Invoke calls a remote exposed callable and returns the raw response body.
// The caller is responsible for decoding the bytes.
func (c *Client) Invoke(ctx context.Context, callID string, payload any) ([]byte, error) {
	reqBody, err := json.Marshal(peerserver.InvokeReq{
		CallID:  callID,
		Payload: payload,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal invoke: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.ServerURL+"/peer/invoke", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.Auth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("invoke request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read invoke body: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusNoContent {
		return nil, fmt.Errorf("invoke failed (%d): %s", resp.StatusCode, string(body))
	}
	return body, nil
}

// InvokeRaw calls a remote exposed callable with a raw binary payload and
// returns the raw response bytes. The caller is responsible for encoding the
// request body and decoding the response body according to the shared schema.
// Use this when the peer transport should carry binary (e.g. TBC) instead of
// JSON.
func (c *Client) InvokeRaw(ctx context.Context, callID string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.ServerURL+"/peer/invoke", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set(peerserver.HeaderPeerCallID, callID)
	c.Auth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("invoke request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read invoke body: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusNoContent {
		return nil, fmt.Errorf("invoke failed (%d): %s", resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// InvokeUnary calls a unary remote callable and decodes the JSON response into
// out. out must be a non-nil pointer.
func (c *Client) InvokeUnary(ctx context.Context, callID string, payload any, out any) error {
	body, err := c.Invoke(ctx, callID, payload)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}
