// server.gen.go — Code generated from appdef. DO NOT EDIT.
// HTTP route registrations for the direct-frontend data path: one
// sdk.RegisterHTTPHandler per frontend-exposed callable (expose: frontend|both).
// The SDK owns the listener and routes (sdk.ServeHTTP: POST /invoke/{id},
// GET /events SSE, static files); this file only fills the dispatch table.
// Callables declared expose: "agent" are agent-facing only and intentionally
// not served over HTTP.
package main

import (
	"encoding/json"

	sdk "github.com/qomos-w/sporemind-plugin-sdk"
)

func init() {
	sdk.RegisterHTTPHandler("provision", func(payload json.RawMessage) (any, error) {
		resp, err := handleProvision(sdk.Request{Payload: payload})
		if err != nil {
			return nil, err
		}
		return resp.Payload, nil
	})
	sdk.RegisterHTTPHandler("codes", func(payload json.RawMessage) (any, error) {
		resp, err := handleCodes(sdk.Request{Payload: payload})
		if err != nil {
			return nil, err
		}
		return resp.Payload, nil
	})
	sdk.RegisterHTTPHandler("verify", func(payload json.RawMessage) (any, error) {
		resp, err := handleVerify(sdk.Request{Payload: payload})
		if err != nil {
			return nil, err
		}
		return resp.Payload, nil
	})
	sdk.RegisterHTTPHandler("remove", func(payload json.RawMessage) (any, error) {
		resp, err := handleRemove(sdk.Request{Payload: payload})
		if err != nil {
			return nil, err
		}
		return resp.Payload, nil
	})
	sdk.RegisterHTTPHandler("update", func(payload json.RawMessage) (any, error) {
		resp, err := handleUpdate(sdk.Request{Payload: payload})
		if err != nil {
			return nil, err
		}
		return resp.Payload, nil
	})
	sdk.RegisterHTTPHandler("discover", func(payload json.RawMessage) (any, error) {
		resp, err := handleDiscover(sdk.Request{Payload: payload})
		if err != nil {
			return nil, err
		}
		return resp.Payload, nil
	})
}
