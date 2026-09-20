// main.gen.go — Code generated from appdef. DO NOT EDIT.
// @generated-hash: 5021136ac7665c26a13cb626844a100e6d9d1594547d4f47bfcc14da2288c64c
package main

import (
	
	sdk "github.com/qomos-w/sporemind-plugin-sdk"
)

func init() {
	sdk.Register(&sdk.Plugin{
		Manifest: sdk.Manifest{
			ID:          "app.tooltest",
			Name:        "ToolTest",
			Version:     "0.1.0",
			Permissions: []string{"llm.invoke"},
			Callables: []sdk.Callable{
					{ID: "forced_tool", Description: "", RequestSchema: "ForcedToolRequest", ResponseSchema: "ForcedToolResponse", Effect: "read", ToolName: "tooltest-forced-tool", Service: "app.tooltest", Streaming: false, TimeoutMs: 240000, Expose: "", Watch: nil},
				},
			Entrypoints: []sdk.Entrypoint{
					{Kind: "view", ID: "main", Title: "ToolTest", Route: "/"},
				},
		},
		OnLoad: func(ctx sdk.Context) error {
			ctx.RegisterCallable("forced_tool", handleForcedTool)
			return nil
		},
		OnUnload: func(ctx sdk.Context) error {
			ctx.UnregisterCallable("forced_tool")
			return nil
		},
	})
}
