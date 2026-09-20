// schemas_gen.go — generated from appdef. DO NOT EDIT.
package main

type ProvisionRequest struct {
	Name string `json:"Name"`
	Secret *string `json:"Secret,omitempty"`
	Digits *int32 `json:"Digits,omitempty"`
	Period *int32 `json:"Period,omitempty"`
	Algorithm *string `json:"Algorithm,omitempty"`
}

type ProvisionResponse struct {
	AccountId string `json:"AccountId"`
	Name string `json:"Name"`
	SecretBase32 string `json:"SecretBase32"`
	OtpAuthUrl string `json:"OtpAuthUrl"`
	Digits int32 `json:"Digits"`
	Period int32 `json:"Period"`
	Algorithm string `json:"Algorithm"`
}

type AccountCode struct {
	AccountId string `json:"AccountId"`
	Name string `json:"Name"`
	Code string `json:"Code"`
	SecretBase32 string `json:"SecretBase32"`
	SecondsRemaining int32 `json:"SecondsRemaining"`
	Period int32 `json:"Period"`
}

type CodesRequest struct {
	AccountId *string `json:"AccountId,omitempty"`
}

type CodesResponse struct {
	Accounts []AccountCode `json:"Accounts"`
	NowUnix int64 `json:"NowUnix"`
}

type VerifyRequest struct {
	AccountId string `json:"AccountId"`
	Code string `json:"Code"`
	Window *int32 `json:"Window,omitempty"`
}

type VerifyResponse struct {
	Valid bool `json:"Valid"`
	AccountId string `json:"AccountId"`
	Error string `json:"Error"`
}

type RemoveRequest struct {
	AccountId string `json:"AccountId"`
}

type RemoveResponse struct {
	Removed bool `json:"Removed"`
	AccountId string `json:"AccountId"`
}

type UpdateRequest struct {
	AccountId string `json:"AccountId"`
	Name *string `json:"Name,omitempty"`
	Secret *string `json:"Secret,omitempty"`
}

type UpdateResponse struct {
	Updated bool `json:"Updated"`
	AccountId string `json:"AccountId"`
	Name string `json:"Name"`
	SecretBase32 string `json:"SecretBase32"`
	OtpAuthUrl string `json:"OtpAuthUrl"`
}

type DiscoverRequest struct {
	Service *string `json:"Service,omitempty"`
	Callable *string `json:"Callable,omitempty"`
	Limit *int32 `json:"Limit,omitempty"`
	Cursor *string `json:"Cursor,omitempty"`
}

type DiscoverMatch struct {
	CallID string `json:"CallID"`
	Service string `json:"Service"`
	Description string `json:"Description"`
	Permission string `json:"Permission"`
	EffectKind string `json:"EffectKind"`
	ReqSchemaId int64 `json:"ReqSchemaId"`
	RespSchemaId int64 `json:"RespSchemaId"`
}

type DiscoverResponse struct {
	Items []DiscoverMatch `json:"Items"`
	NextCursor string `json:"NextCursor"`
}
