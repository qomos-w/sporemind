package domain

// AgentGetSessionResp is the response for agent.session.get.
// Returns both committed turns and the active in-progress turn snapshot.
type AgentGetSessionResp struct {
	Turns            []Turn      `json:"Turns"`
	ActiveTurn       *TurnStatus `json:"ActiveTurn,omitempty"`
	ActiveTurnEvents []TurnEvent `json:"ActiveTurnEvents,omitempty"`
}
