package domain

// --- turn lifecycle state maps ---

var validTurnStates = map[string]struct{}{
	TurnStateRunning:   {},
	TurnStatePaused:    {},
	TurnStateCompleted: {},
	TurnStateFailed:    {},
	TurnStateCancelled: {},
	TurnStateAbandoned: {},
	TurnStateWaiting:   {},
}

var validPauseReasons = map[string]struct{}{
	PauseReasonUser:         {},
	PauseReasonInteraction:  {},
	PauseReasonPermission:   {},
	PauseReasonPlanApproval: {},
	PauseReasonGoalInput:    {},
	PauseReasonRecovery:     {},
}

var lifecycleStateByKind = map[string]string{
	TurnLifecycleStarted:   TurnStateRunning,
	TurnLifecyclePaused:    TurnStatePaused,
	TurnLifecycleResumed:   TurnStateRunning,
	TurnLifecycleCompleted: TurnStateCompleted,
	TurnLifecycleFailed:    TurnStateFailed,
	TurnLifecycleCancelled: TurnStateCancelled,
	TurnLifecycleAbandoned: TurnStateAbandoned,
	TurnLifecycleWaiting:   TurnStateWaiting,
}
