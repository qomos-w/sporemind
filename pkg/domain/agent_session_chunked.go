package domain

import (
	"reflect"

	"github.com/qomos-w/spore/schema"
)

const (
	// Chunked agent session export/import schema IDs. Picked above the
	// current registry range to avoid collisions until the schemas are
	// regenerated from source.
	AgentSessionExportRangeReqSchemaID  uint64 = 2100
	AgentSessionExportRangeRespSchemaID uint64 = 2101
	AgentSessionImportTurnsReqSchemaID  uint64 = 2102
	AgentSessionImportTurnsRespSchemaID uint64 = 2103
)

func init() {
	schema.RegisterStructType(AgentSessionExportRangeReqSchemaID, reflect.TypeOf(AgentSessionExportRangeReq{}))
	schema.RegisterStructType(AgentSessionExportRangeRespSchemaID, reflect.TypeOf(AgentSessionExportRangeResp{}))
	schema.RegisterStructType(AgentSessionImportTurnsReqSchemaID, reflect.TypeOf(AgentSessionImportTurnsReq{}))
	schema.RegisterStructType(AgentSessionImportTurnsRespSchemaID, reflect.TypeOf(AgentSessionImportTurnsResp{}))
}

// AgentSessionExportRangeReq requests a slice of older turns from an agent
// session. BeforeTurnID is the cursor: the response contains turns strictly
// before this turn. Empty BeforeTurnID means "from the end".
type AgentSessionExportRangeReq struct {
	BeforeTurnID string `json:"BeforeTurnId,omitempty"`
	Limit        int32  `json:"Limit,omitempty"`
}

// AgentSessionExportRangeResp contains a chunk of session history suitable for
// appending to another agent session via AgentSessionImportTurnsReq.
type AgentSessionExportRangeResp struct {
	Turns            []Turn           `json:"Turns,omitempty"`
	Steps            []Step           `json:"Steps,omitempty"`
	SummarySegments  []SummarySegment `json:"SummarySegments,omitempty"`
	ExploreResults   []ExploreResult  `json:"ExploreResults,omitempty"`
	HasMore          bool             `json:"HasMore"`
	TotalTurns       int32            `json:"TotalTurns,omitempty"`
	NextBeforeTurnID string           `json:"NextBeforeTurnId,omitempty"`
	NextSeq          int64            `json:"NextSeq,omitempty"`
	NextTurnOrder    int64            `json:"NextTurnOrder,omitempty"`
	NextIdx          int32            `json:"NextIdx,omitempty"`
}

// AgentSessionImportTurnsReq appends a chunk of older turns to an existing
// session. The turns must be older than any turns already in the target
// session; they are prepended to the turn list so chronological order is preserved.
type AgentSessionImportTurnsReq struct {
	Turns            []Turn           `json:"Turns,omitempty"`
	Steps            []Step           `json:"Steps,omitempty"`
	SummarySegments  []SummarySegment `json:"SummarySegments,omitempty"`
	ExploreResults   []ExploreResult  `json:"ExploreResults,omitempty"`
	IsFinal          bool             `json:"IsFinal,omitempty"`
	SourceAgentID    string           `json:"SourceAgentId,omitempty"`
	SourceTotalTurns int32            `json:"SourceTotalTurns,omitempty"`
	NextSeq          int64            `json:"NextSeq,omitempty"`
	NextTurnOrder    int64            `json:"NextTurnOrder,omitempty"`
	NextIdx          int32            `json:"NextIdx,omitempty"`
}

// AgentSessionImportTurnsResp reports how many turns were accepted.
type AgentSessionImportTurnsResp struct {
	AcceptedTurns int32 `json:"AcceptedTurns"`
}

// CloneState tracks the progress of an asynchronous agent clone. It is
// persisted by the workspace actor and keyed by the cloned agent's actor ID.
type CloneState struct {
	SourceActorID    string `json:"SourceActorID"`
	SourceTotalTurns int32  `json:"SourceTotalTurns"`
	PendingHistory   bool   `json:"PendingHistory"`
	SourceNextSeq    int64  `json:"SourceNextSeq,omitempty"`
	SourceNextIdx    int32  `json:"SourceNextIdx,omitempty"`
}
