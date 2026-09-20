// main.gen.go — Code generated from appdef. DO NOT EDIT.
// @generated-hash: 9f8cdcb35339736142b0fb33181ca263203f54634d44fd6fd29aa1be429d97ab
package main

import (
		"encoding/json"

	sdk "github.com/qomos-w/sporemind-plugin-sdk"
)

func init() {
	sdk.Register(&sdk.Plugin{
		Manifest: sdk.Manifest{
			ID:          "app.authenticator",
			Name:        "Authenticator",
			Version:     "0.1.0",
			Permissions: []string{"app.state", "registry.read"},
			Callables: []sdk.Callable{
					{ID: "provision", Description: "", RequestSchema: "ProvisionRequest", ResponseSchema: "ProvisionResponse", Effect: "mutate", ToolName: "authenticator-provision", Service: "app.authenticator", Streaming: false, TimeoutMs: 0, Expose: "", Watch: nil},
					{ID: "codes", Description: "", RequestSchema: "CodesRequest", ResponseSchema: "CodesResponse", Effect: "read", ToolName: "authenticator-codes", Service: "app.authenticator", Streaming: false, TimeoutMs: 0, Expose: "", Watch: nil},
					{ID: "verify", Description: "", RequestSchema: "VerifyRequest", ResponseSchema: "VerifyResponse", Effect: "read", ToolName: "authenticator-verify", Service: "app.authenticator", Streaming: false, TimeoutMs: 0, Expose: "", Watch: nil},
					{ID: "remove", Description: "", RequestSchema: "RemoveRequest", ResponseSchema: "RemoveResponse", Effect: "mutate", ToolName: "authenticator-remove", Service: "app.authenticator", Streaming: false, TimeoutMs: 0, Expose: "", Watch: nil},
					{ID: "update", Description: "", RequestSchema: "UpdateRequest", ResponseSchema: "UpdateResponse", Effect: "mutate", ToolName: "authenticator-update", Service: "app.authenticator", Streaming: false, TimeoutMs: 0, Expose: "", Watch: nil},
					{ID: "discover", Description: "discover is programmatic (not agent-facing): it exercises the registry.read capability through sdk.ListCallables.", RequestSchema: "DiscoverRequest", ResponseSchema: "DiscoverResponse", Effect: "read", ToolName: "authenticator-discover", Service: "app.authenticator", Streaming: false, TimeoutMs: 0, Expose: "", Watch: nil},
				},
			Entrypoints: []sdk.Entrypoint{
					{Kind: "view", ID: "main", Title: "Authenticator", Route: "/"},
				},
		},
		OnLoad: func(ctx sdk.Context) error {
			ctx.RegisterCallable("provision", handleProvision)
			ctx.RegisterCallable("codes", handleCodes)
			ctx.RegisterCallable("verify", handleVerify)
			ctx.RegisterCallable("remove", handleRemove)
			ctx.RegisterCallable("update", handleUpdate)
			ctx.RegisterCallable("discover", handleDiscover)
			ctx.RegisterEventListener("app_lifecycle", func(payload json.RawMessage) error {
				var ev AppLifecycleEvent
				if err := json.Unmarshal(payload, &ev); err != nil {
					return err
				}
				return OnAppLifecycle(ev)
			})
			ctx.RegisterEventListener("app_event", func(payload json.RawMessage) error {
				var ev AppEventMessage
				if err := json.Unmarshal(payload, &ev); err != nil {
					return err
				}
				return OnAppEvent(ev)
			})
			return nil
		},
		OnUnload: func(ctx sdk.Context) error {
			ctx.UnregisterCallable("provision")
			ctx.UnregisterCallable("codes")
			ctx.UnregisterCallable("verify")
			ctx.UnregisterCallable("remove")
			ctx.UnregisterCallable("update")
			ctx.UnregisterCallable("discover")
			return nil
		},
	})
}
