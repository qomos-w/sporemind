// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function *dispatch(client: GosporeClient, req: systemTypes.SendSessionMessageReq, opts?: InvokeOptions): AsyncIterable<systemTypes.AggregatorChunk> {
  yield* client.subscribe<systemTypes.AggregatorChunk>("aiaggregator.dispatch", req, { reqSchemaId: 347, chunkSchemaId: 369, ...opts });
}

export async function intent(client: GosporeClient, req: systemTypes.SendSessionMessageReq, opts?: InvokeOptions): Promise<systemTypes.SummarizeResp> {
  return client.invoke<systemTypes.SendSessionMessageReq, systemTypes.SummarizeResp>("aiaggregator.intent", req, { reqSchemaId: 347, resSchemaId: 348, ...opts });
}

export const intent_meta = {
  callable: "aiaggregator.intent",
  name: "intent",
  reqSchemaId: 347,
  resSchemaId: 348,
} as const;

export async function probeTokens(client: GosporeClient, req: systemTypes.SendSessionMessageReq, opts?: InvokeOptions): Promise<systemTypes.ProbeTokensResp> {
  return client.invoke<systemTypes.SendSessionMessageReq, systemTypes.ProbeTokensResp>("aiaggregator.probe_tokens", req, { reqSchemaId: 347, resSchemaId: 349, ...opts });
}

export const probeTokens_meta = {
  callable: "aiaggregator.probe_tokens",
  name: "probe_tokens",
  reqSchemaId: 347,
  resSchemaId: 349,
} as const;

export async function status(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.AIAggregatorStatusResp> {
  return client.invoke<void, systemTypes.AIAggregatorStatusResp>("aiaggregator.status", undefined, { resSchemaId: 353, ...opts });
}

export async function summarize(client: GosporeClient, req: systemTypes.SendSessionMessageReq, opts?: InvokeOptions): Promise<systemTypes.SummarizeResp> {
  return client.invoke<systemTypes.SendSessionMessageReq, systemTypes.SummarizeResp>("aiaggregator.summarize", req, { reqSchemaId: 347, resSchemaId: 348, ...opts });
}

export const summarize_meta = {
  callable: "aiaggregator.summarize",
  name: "summarize",
  reqSchemaId: 347,
  resSchemaId: 348,
} as const;

export async function toolJudge(client: GosporeClient, req: systemTypes.SendSessionMessageReq, opts?: InvokeOptions): Promise<systemTypes.SummarizeResp> {
  return client.invoke<systemTypes.SendSessionMessageReq, systemTypes.SummarizeResp>("aiaggregator.tool_judge", req, { reqSchemaId: 347, resSchemaId: 348, ...opts });
}

export const toolJudge_meta = {
  callable: "aiaggregator.tool_judge",
  name: "tool_judge",
  reqSchemaId: 347,
  resSchemaId: 348,
} as const;

