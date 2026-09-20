// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { GlassScene } from './glass.scene';

export interface GlassCapability {
  Name: string;
  Version?: string | undefined;
  Features?: string[] | undefined;
}

export interface GlassRenderFrame {
  Text?: string | undefined;
  ImageUrl?: string | undefined;
  Scene?: GlassScene | undefined;
  Layout?: string | undefined;
  Meta?: string | undefined;
}

export interface GlassBootstrapReq {
  Key?: string | undefined;
  Username?: string | undefined;
  Password?: string | undefined;
  DeviceId?: string | undefined;
  Capabilities?: GlassCapability[] | undefined;
}

export interface GlassBootstrapResp {
  SessionId: string;
  DeviceId: string;
  Token: string;
  ExpiresAt: string;
  Kind: string;
  Scope: string;
}

export interface GlassSessionClaimReq {
  SessionId: string;
  DeviceId: string;
  Capabilities?: GlassCapability[] | undefined;
}

export interface GlassSessionClaimResp {
  SessionId: string;
  DeviceId: string;
  Generation: number;
  Online: boolean;
  Reconnected: boolean;
  Replaced: boolean;
  LastFrame?: GlassRenderFrame | undefined;
}

export interface GlassLifecycleEvent {
  Kind: string;
  SessionId: string;
  DeviceId: string;
  Generation: number;
  Reason?: string | undefined;
  Timestamp?: string | undefined;
}

export interface GlassSessionState {
  SessionId: string;
  DeviceId: string;
  Generation: number;
  Online: boolean;
  LastSeen?: string | undefined;
  OfflineAt?: string | undefined;
  LastFrame?: GlassRenderFrame | undefined;
  Capabilities: GlassCapability[];
}

export interface GlassGetStateReq {

}

export interface GlassGetStateResp {
  Active: boolean;
  Session?: GlassSessionState | undefined;
}

export interface GlassSpeechStartReq {
  SessionId: string;
  Generation: number;
  UtteranceId: string;
  SampleRate?: number | undefined;
  Channels?: number | undefined;
  BitsPerSample?: number | undefined;
}

export interface GlassSpeechChunkReq {
  SessionId: string;
  Generation: number;
  UtteranceId: string;
  Sequence: number;
  Pcm: Uint8Array;
}

export interface GlassSpeechEndReq {
  SessionId: string;
  Generation: number;
  UtteranceId: string;
}

export interface GlassSpeechAck {
  SessionId: string;
  UtteranceId: string;
  HighestContiguousSeq: number;
  EndReceived?: boolean | undefined;
  Accepted?: boolean | undefined;
  Reason?: string | undefined;
}

export interface GlassSpeechStartResp {
  SessionId: string;
  UtteranceId: string;
  Ack: GlassSpeechAck;
}

export interface GlassSpeechEndResp {
  SessionId: string;
  UtteranceId: string;
  Ack: GlassSpeechAck;
}

export interface GlassTranscript {
  SessionId: string;
  UtteranceId: string;
  Generation: number;
  DeviceId: string;
  Text: string;
  Error?: string | undefined;
  Timestamp: string;
}

export interface GlassTranscriptEvent {
  Transcript: GlassTranscript;
}

export interface GlassTranscriptLatestReq {

}

export interface GlassTranscriptLatestResp {
  Transcript?: GlassTranscript | undefined;
}

export interface GlassRenderReq {
  Frame: GlassRenderFrame;
  SessionId?: string | undefined;
  Generation?: number | undefined;
}

export interface GlassRenderResp {
  SessionId: string;
  Generation: number;
  Applied: boolean;
  Timestamp?: string | undefined;
}

export interface GlassRenderEvent {
  SessionId: string;
  Generation: number;
  Frame: GlassRenderFrame;
  Timestamp?: string | undefined;
}

export interface GlassSpeakReq {
  Text: string;
  MessageId: string;
  SessionId?: string | undefined;
  Generation?: number | undefined;
}

export interface GlassSpeakResp {
  SessionId: string;
  Generation: number;
  MessageId: string;
  Duplicate: boolean;
  Timestamp?: string | undefined;
}

export interface GlassSpeakEvent {
  SessionId: string;
  Generation: number;
  MessageId: string;
  Text: string;
  Duplicate: boolean;
  Timestamp?: string | undefined;
}

export interface GlassCapabilitiesState {
  Active: boolean;
  SessionId?: string | undefined;
  DeviceId?: string | undefined;
  Generation?: number | undefined;
  Capabilities: GlassCapability[];
}

export interface GlassCapabilitiesReq {

}

export interface GlassCapabilitiesResp {
  State: GlassCapabilitiesState;
}

export interface GlassDebugTimelineEntry {
  Kind: string;
  Detail?: string | undefined;
  Timestamp?: string | undefined;
}

export interface GlassDebugStats {
  Utterances: number;
  Transcripts: number;
  Renders: number;
  Speaks: number;
  SpeakDeduped: number;
  Errors: number;
}

export interface GlassDebugState {
  Session?: GlassSessionState | undefined;
  Capabilities: GlassCapability[];
  CurrentFrame?: GlassRenderFrame | undefined;
  AudioAck?: GlassSpeechAck | undefined;
  Stats: GlassDebugStats;
  Timeline: GlassDebugTimelineEntry[];
  Inbox?: GlassInboxSnapshot | undefined;
  Hud?: GlassHudStatus | undefined;
  Deliveries: GlassDeliveryLogEntry[];
}

export interface GlassDebugReq {

}

export interface GlassDebugResp {
  State: GlassDebugState;
}

export interface GlassEvent {
  EventId: string;
  Source: string;
  Kind: string;
  Priority: string;
  Payload: string;
  DedupeKey?: string | undefined;
  State: string;
  CreatedAt: string;
  TTLAt?: string | undefined;
  RetryAt?: string | undefined;
  Retries: number;
  InFlightAt?: string | undefined;
  CompletedAt?: string | undefined;
  Outcome?: string | undefined;
}

export interface GlassEventEnqueueReq {
  Source: string;
  Kind: string;
  Priority: string;
  Payload: string;
  DedupeKey?: string | undefined;
}

export interface GlassEventEnqueueResp {
  Accepted: boolean;
  EventId?: string | undefined;
  Reason?: string | undefined;
}

export interface GlassEventCompleteReq {
  EventId: string;
  Outcome: string;
  Reason?: string | undefined;
}

export interface GlassEventCompleteResp {
  EventId: string;
  State: string;
}

export interface GlassEventDeliverReq {
  EventId: string;
  Source: string;
  Kind: string;
  Priority: string;
  Payload: string;
  Deliveries?: number | undefined;
}

export interface GlassEventDeliverResp {
  EventId: string;
  Received: boolean;
}

export interface GlassCoordinatorGate {
  Running: boolean;
  ActiveCall: boolean;
}

export interface GlassInboxSnapshot {
  Total: number;
  Pending: number;
  InFlight: number;
  Completed: number;
  Ignored: number;
  Expired: number;
  Events: GlassEvent[];
}

export interface GlassInboxReq {

}

export interface GlassInboxResp {
  Snapshot: GlassInboxSnapshot;
}

export interface GlassEventEnqueuedEvent {
  Event: GlassEvent;
}

export interface GlassEventDeliveredEvent {
  Event: GlassEvent;
}

export interface GlassEventCompletedEvent {
  Event: GlassEvent;
}

export interface GlassDeliveryLogEntry {
  EventId: string;
  Success: boolean;
  Detail?: string | undefined;
  Timestamp?: string | undefined;
}

export interface GlassHudStatus {
  Connection: string;
  BatteryLevel?: number | undefined;
  Charging?: boolean | undefined;
  AgentRunning: boolean;
  AgentName?: string | undefined;
  AgentState?: string | undefined;
  AgentOutcome?: string | undefined;
  Timestamp?: string | undefined;
}
