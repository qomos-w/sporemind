package domain

import "time"

// DefaultInvokeTimeout is the standard timeout for inter-actor invoke calls.
const DefaultInvokeTimeout = 15 * time.Second

// ToolCallTimeout is the timeout for a single tool execution via planner.Call.
const ToolCallTimeout = 5 * time.Minute

// ApprovalTimeout is the timeout for agent permission review (may involve human).
const ApprovalTimeout = 5 * time.Minute

// StreamIdleTimeout is how long an LLM stream may go silent before the FIRST
// chunk arrives. Reasoning models (DeepSeek-R1, kimi-k2-thinking, etc.) can
// think for 60-90s before emitting the first token; 3 minutes covers the p99
// of legitimate first-token latency without holding a dead stream open forever.
//
// Once streaming has begun, inter-chunk silence is governed by the much
// shorter StreamChunkIdleTimeout below.
const StreamIdleTimeout = 3 * time.Minute

// StreamChunkIdleTimeout is the max silence between consecutive stream chunks
// AFTER the first chunk has arrived. The first-chunk wait is governed by
// StreamIdleTimeout (or PlanGoalStreamIdleTimeout). Reasoning providers may
// pause token emission while continuing inference, so this window must cover
// legitimate mid-stream thinking rather than treating every short gap as a
// dead connection.
const StreamChunkIdleTimeout = 90 * time.Second

// PlanGoalStreamIdleTimeout is the extended idle timeout used when the current
// dispatch exposes plan_submit or goal_submit tools. Generating a thorough plan
// or goal interpretation can involve deep reasoning that runs well past the
// normal 3-minute idle budget; 15 minutes covers the p99 of plan/goal
// generation without holding a dead stream open forever.
const PlanGoalStreamIdleTimeout = 15 * time.Minute

// NestedAggregatorOpenTimeout bounds the parent's wait for a nested child
// aggregator's stream-open (first resolved_unit chunk). A child whose whole
// pool is unreachable rotates unit-by-unit inside this single wait — without
// a dedicated bound the wait consumes the full StreamIdleTimeout budget while
// each doomed unit burns dial+TLS+header timeouts. On expiry the parent
// registers the child short-term unavailable and rotates to its own next
// candidate. 90s still covers a slow-but-healthy nested open (the child's own
// first-chunk wait is bounded by dial 10s + TLS 10s + response-header 30s).
const NestedAggregatorOpenTimeout = 90 * time.Second

// ChildAgentIdleTimeout is how long a forked child agent may go without a
// heartbeat before the parent considers it lost (crashed/panicked/disconnected).
//
// The parent polls the child stateless agent_status callable every
// ChildHeartbeatInterval and compares its child-owned atomic activity timestamp.
// The child updates that timestamp only on real turn activity (turn start, LLM
// output, and tool completion), so progress delivery does not affect timeout.
const ChildAgentIdleTimeout = 2 * time.Minute

// ChildHeartbeatInterval is the parent-side polling interval for a forked
// child's stateless liveness snapshot. It is well below ChildAgentIdleTimeout
// to detect a child that has stopped producing real activity promptly.
const ChildHeartbeatInterval = 15 * time.Second

// ChildLivenessFailureThreshold is the number of consecutive liveness-check
// failures (agent_status call errors) after which a forked child is evicted
// immediately rather than waiting for ChildAgentIdleTimeout. At the default
// 15s polling interval this gives a ~30s window — long enough to ride out a
// single transient error, short enough to unblock the parent promptly when a
// child is deleted or has crashed.
const ChildLivenessFailureThreshold = 2
