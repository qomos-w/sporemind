// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function bootstrap(client: GosporeClient, req: systemTypes.GlassBootstrapReq, opts?: InvokeOptions): Promise<systemTypes.GlassBootstrapResp> {
  return client.invoke<systemTypes.GlassBootstrapReq, systemTypes.GlassBootstrapResp>("glass_interact.bootstrap", req, { reqSchemaId: 3890, resSchemaId: 3891, ...opts });
}

export const bootstrap_meta = {
  callable: "glass_interact.bootstrap",
  name: "bootstrap",
  reqSchemaId: 3890,
  resSchemaId: 3891,
} as const;

export async function debugSimulate(client: GosporeClient, req: systemTypes.GlassDebugSimulateReq, opts?: InvokeOptions): Promise<systemTypes.GlassDebugSimulateResp> {
  return client.invoke<systemTypes.GlassDebugSimulateReq, systemTypes.GlassDebugSimulateResp>("glass_interact.debug_simulate", req, { reqSchemaId: 4128, resSchemaId: 4129, ...opts });
}

export const debugSimulate_meta = {
  callable: "glass_interact.debug_simulate",
  name: "debug_simulate",
  reqSchemaId: 4128,
  resSchemaId: 4129,
} as const;

export async function debugState(client: GosporeClient, req: systemTypes.GlassDebugReq, opts?: InvokeOptions): Promise<systemTypes.GlassDebugResp> {
  return client.invoke<systemTypes.GlassDebugReq, systemTypes.GlassDebugResp>("glass_interact.debug_state", req, { reqSchemaId: 3920, resSchemaId: 3921, ...opts });
}

export const debugState_meta = {
  callable: "glass_interact.debug_state",
  name: "debug_state",
  reqSchemaId: 3920,
  resSchemaId: 3921,
} as const;

export async function interactionReport(client: GosporeClient, req: systemTypes.GlassInteractionReportReq, opts?: InvokeOptions): Promise<systemTypes.GlassInteractionReportResp> {
  return client.invoke<systemTypes.GlassInteractionReportReq, systemTypes.GlassInteractionReportResp>("glass_interact.interaction_report", req, { reqSchemaId: 4037, resSchemaId: 4038, ...opts });
}

export const interactionReport_meta = {
  callable: "glass_interact.interaction_report",
  name: "interaction_report",
  reqSchemaId: 4037,
  resSchemaId: 4038,
} as const;

export async function sessionClaim(client: GosporeClient, req: systemTypes.GlassSessionClaimReq, opts?: InvokeOptions): Promise<systemTypes.GlassSessionClaimResp> {
  return client.invoke<systemTypes.GlassSessionClaimReq, systemTypes.GlassSessionClaimResp>("glass_interact.session_claim", req, { reqSchemaId: 3892, resSchemaId: 3893, ...opts });
}

export const sessionClaim_meta = {
  callable: "glass_interact.session_claim",
  name: "session_claim",
  reqSchemaId: 3892,
  resSchemaId: 3893,
} as const;

export async function sessionGetState(client: GosporeClient, req: systemTypes.GlassGetStateReq, opts?: InvokeOptions): Promise<systemTypes.GlassGetStateResp> {
  return client.invoke<systemTypes.GlassGetStateReq, systemTypes.GlassGetStateResp>("glass_interact.session_get_state", req, { reqSchemaId: 3896, resSchemaId: 3897, ...opts });
}

export const sessionGetState_meta = {
  callable: "glass_interact.session_get_state",
  name: "session_get_state",
  reqSchemaId: 3896,
  resSchemaId: 3897,
} as const;

export async function speechChunk(client: GosporeClient, req: systemTypes.GlassSpeechChunkReq, opts?: InvokeOptions): Promise<systemTypes.GlassSpeechAck> {
  return client.invoke<systemTypes.GlassSpeechChunkReq, systemTypes.GlassSpeechAck>("glass_interact.speech_chunk", req, { reqSchemaId: 3899, resSchemaId: 3901, ...opts });
}

export const speechChunk_meta = {
  callable: "glass_interact.speech_chunk",
  name: "speech_chunk",
  reqSchemaId: 3899,
  resSchemaId: 3901,
} as const;

export async function speechEnd(client: GosporeClient, req: systemTypes.GlassSpeechEndReq, opts?: InvokeOptions): Promise<systemTypes.GlassSpeechEndResp> {
  return client.invoke<systemTypes.GlassSpeechEndReq, systemTypes.GlassSpeechEndResp>("glass_interact.speech_end", req, { reqSchemaId: 3900, resSchemaId: 3903, ...opts });
}

export const speechEnd_meta = {
  callable: "glass_interact.speech_end",
  name: "speech_end",
  reqSchemaId: 3900,
  resSchemaId: 3903,
} as const;

export async function speechStart(client: GosporeClient, req: systemTypes.GlassSpeechStartReq, opts?: InvokeOptions): Promise<systemTypes.GlassSpeechStartResp> {
  return client.invoke<systemTypes.GlassSpeechStartReq, systemTypes.GlassSpeechStartResp>("glass_interact.speech_start", req, { reqSchemaId: 3898, resSchemaId: 3902, ...opts });
}

export const speechStart_meta = {
  callable: "glass_interact.speech_start",
  name: "speech_start",
  reqSchemaId: 3898,
  resSchemaId: 3902,
} as const;

export async function telemetryReport(client: GosporeClient, req: systemTypes.GlassTelemetryReq, opts?: InvokeOptions): Promise<systemTypes.GlassTelemetryResp> {
  return client.invoke<systemTypes.GlassTelemetryReq, systemTypes.GlassTelemetryResp>("glass_interact.telemetry_report", req, { reqSchemaId: 4080, resSchemaId: 4081, ...opts });
}

export const telemetryReport_meta = {
  callable: "glass_interact.telemetry_report",
  name: "telemetry_report",
  reqSchemaId: 4080,
  resSchemaId: 4081,
} as const;

