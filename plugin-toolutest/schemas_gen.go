// schemas_gen.go — generated from appdef. DO NOT EDIT.
package main

type ForcedToolRequest struct {
	Prompt string `json:"Prompt"`
	Provider *string `json:"Provider,omitempty"`
	Model *string `json:"Model,omitempty"`
	Choice *string `json:"Choice,omitempty"`
	Mode *string `json:"Mode,omitempty"`
}

type ForcedToolResponse struct {
	ToolName string `json:"ToolName"`
	Arguments string `json:"Arguments"`
	StopInfo string `json:"StopInfo"`
	Raw string `json:"Raw"`
}
