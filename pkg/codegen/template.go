package codegen

// templateAppID is the placeholder app id used by the default template
// scaffold (defaultAppdefContent below). ensureGoModContent upgrades a go.mod
// module line still carrying this placeholder to the real app id.
const templateAppID = "app.default"

// defaultAppdefContent is the minimal .appdef produced for empty projects.
// It is the canonical syntax reference: one typed callable (named-struct
// request/response references), the `optional` prefix modifier, and a view
// entrypoint — all inside the app { } block. Agents bootstrap by scaffolding
// this, reading it, and rewriting it for their app.
const defaultAppdefContent = `app Default {
    id:        "app.default"
    name:      "Default"
    version:   "0.1.0"
    namespace: "default"

    // Structs are declared inside the app block and referenced by name from
    // callables. ` + "`optional`" + ` is a PREFIX modifier; the field becomes a
    // pointer (*string) in the generated schemas_gen.go.
    struct PingRequest {
        Message:       string   // required
        optional Style: string   // optional (prefix form)
    }
    struct PingResponse {
        Pong: string
    }

    // request/response attach via named-struct reference — inline request
    // blocks inside the callable are silently dropped.
    callable ping {
        request:  PingRequest
        response: PingResponse
        effect:   "read"
        toolName: "default-ping"
    }

    // Entrypoint syntax: kind is the block label, the id follows it.
    entrypoint view main {
        title: "Default App"
        route: "/"
    }
}
`

// defaultHandlersGo is the template produced for empty projects (no .appdef
// files). It implements the declared ping callable with the full typed loop:
// json.Unmarshal(req.Payload) into the GENERATED PingRequest struct (from
// schemas_gen.go — never redefine appdef structs here), business logic, then
// the response struct passed straight through Response.Payload.
const defaultHandlersGo = `// handlers.go — created by generator; agent-owned afterwards.
// This is the default template for empty projects. The app.appdef alongside
// declares one typed callable (ping: PingRequest → PingResponse); the
// request/response structs live in schemas_gen.go, which is regenerated from
// app.appdef — do not redefine them here.
package main

import (
	"encoding/json"

	sdk "github.com/qomos-w/sporemind-plugin-sdk"
)

// handlePing demonstrates the typed handler loop: decode req.Payload into
// the generated request struct, run business logic, return the generated
// response struct in Response.Payload.
func handlePing(req sdk.Request) (sdk.Response, error) {
	var payload PingRequest
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		return sdk.Response{}, err
	}
	style := "default"
	if payload.Style != nil && *payload.Style != "" {
		style = *payload.Style
	}
	return sdk.Response{Payload: PingResponse{
		Pong: payload.Message + " (" + style + ")",
	}}, nil
}
`
