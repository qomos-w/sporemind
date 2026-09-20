package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
)

// scriptedDbRef answers Invoke per callID with canned raw JSON responses,
// standing in for the dbmanager service ref behind db.profile_list and
// db.profile_dial.
type scriptedDbRef struct {
	responses map[string]string
	calls     []string
}

func (r *scriptedDbRef) ID() id.ActorID          { return id.ActorID{} }
func (r *scriptedDbRef) Service() (string, bool) { return "dbmanager", true }
func (r *scriptedDbRef) Invoke(_ context.Context, callID string, _ any, _ ...map[string]string) *invoke.Call {
	r.calls = append(r.calls, callID)
	body, ok := r.responses[callID]
	if !ok {
		return invoke.NewCall(invoke.CallModeUnary, &rawStream{err: errors.New("unexpected callID " + callID)})
	}
	return invoke.NewCall(invoke.CallModeUnary, &rawStream{body: []byte(body)})
}

type rawStream struct {
	body []byte
	err  error
	done bool
}

func (s *rawStream) Recv() (any, error) { return nil, errors.New("value mode unsupported") }
func (s *rawStream) RecvRaw() ([]byte, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.done {
		return nil, errors.New("single value only")
	}
	s.done = true
	return s.body, nil
}
func (s *rawStream) Close() error { return nil }

func TestHandleHostBridgeDbProfileList(t *testing.T) {
	a := &Actor{}
	if _, err := a.handleHostBridgeDbProfileList(nil); err == nil {
		t.Fatal("expected error when dbmanager service is unavailable")
	}
	svc := &scriptedDbRef{responses: map[string]string{
		"dbmanager.profile_list": `{"Items":[{"Id":"p1","Name":"local mongo","Backend":"mongo","Endpoint":"127.0.0.1:27017","HasPassword":false,"HasSecret":false,"HasToken":false}]}`,
	}}
	raw, err := a.handleHostBridgeDbProfileList(svc)
	if err != nil {
		t.Fatalf("db.profile_list: %v", err)
	}
	if !strings.Contains(string(raw), `"Id":"p1"`) {
		t.Errorf("response missing profile: %s", raw)
	}
	if len(svc.calls) != 1 || svc.calls[0] != "dbmanager.profile_list" {
		t.Errorf("calls = %v, want [dbmanager.profile_list]", svc.calls)
	}
}

func TestHandleHostBridgeDbProfileDial(t *testing.T) {
	a := &Actor{}
	if _, err := a.handleHostBridgeDbProfileDial(nil, []byte(`{"id":"p1"}`)); err == nil {
		t.Fatal("expected error when dbmanager service is unavailable")
	}
	if _, err := a.handleHostBridgeDbProfileDial(&scriptedDbRef{}, []byte(`{}`)); err == nil {
		t.Fatal("expected error for empty id")
	}
	svc := &scriptedDbRef{responses: map[string]string{
		"dbmanager.profile_lookup":  `{"Profile":{"Id":"p1","Name":"local mongo","Backend":"mongo","Endpoint":"127.0.0.1:27017","Database":"sporemind","Username":"root"}}`,
		"dbmanager.profile_resolve": `{"Username":"root","Password":"s3cret"}`,
	}}
	raw, err := a.handleHostBridgeDbProfileDial(svc, []byte(`{"id":"p1"}`))
	if err != nil {
		t.Fatalf("db.profile_dial: %v", err)
	}
	var resp struct {
		Backend    string `json:"backend"`
		Endpoint   string `json:"endpoint"`
		Database   string `json:"database"`
		DialAddr   string `json:"dial_addr"`
		TLS        bool   `json:"tls"`
		AuthSource string `json:"auth_source"`
		Username   string `json:"username"`
		Password   string `json:"password"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode resp: %v", err)
	}
	if resp.Backend != "mongo" || resp.DialAddr != "127.0.0.1:27017" || resp.Database != "sporemind" {
		t.Errorf("unexpected dial params: %+v", resp)
	}
	if resp.TLS {
		t.Error("loopback endpoint must default to TLS off")
	}
	if resp.Username != "root" || resp.Password != "s3cret" {
		t.Errorf("credential not resolved: %+v", resp)
	}
	if resp.AuthSource != "admin" {
		t.Errorf("mongo AuthSource = %q, want admin", resp.AuthSource)
	}
}

func TestHandleHostBridgeDbProfileDialTLS(t *testing.T) {
	a := &Actor{}
	svc := &scriptedDbRef{responses: map[string]string{
		"dbmanager.profile_lookup":  `{"Profile":{"Id":"p2","Name":"prod","Backend":"mongo","Endpoint":"mongo.example.com:27017"}}`,
		"dbmanager.profile_resolve": `{}`,
	}}
	raw, err := a.handleHostBridgeDbProfileDial(svc, []byte(`{"id":"p2"}`))
	if err != nil {
		t.Fatalf("db.profile_dial: %v", err)
	}
	var resp struct {
		TLS           bool   `json:"tls"`
		TLSServerName string `json:"tls_server_name"`
		AuthSource    string `json:"auth_source"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode resp: %v", err)
	}
	if !resp.TLS {
		t.Error("non-loopback direct endpoint must default to TLS on")
	}
	if resp.TLSServerName != "mongo.example.com" {
		t.Errorf("TLSServerName = %q, want mongo.example.com", resp.TLSServerName)
	}
	if resp.AuthSource != "" {
		t.Errorf("AuthSource = %q, want empty without username", resp.AuthSource)
	}
}
