// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function cancel(client: GosporeClient, req: systemTypes.CancelReq, opts?: InvokeOptions): Promise<systemTypes.Task> {
  return client.invoke<systemTypes.CancelReq, systemTypes.Task>("crawl.cancel", req, { reqSchemaId: 1721, resSchemaId: 139, ...opts });
}

export const cancel_meta = {
  callable: "crawl.cancel",
  name: "cancel",
  reqSchemaId: 1721,
  resSchemaId: 139,
} as const;

export async function get(client: GosporeClient, req: systemTypes.GetReq, opts?: InvokeOptions): Promise<systemTypes.Task> {
  return client.invoke<systemTypes.GetReq, systemTypes.Task>("crawl.get", req, { reqSchemaId: 1661, resSchemaId: 139, ...opts });
}

export const get_meta = {
  callable: "crawl.get",
  name: "get",
  reqSchemaId: 1661,
  resSchemaId: 139,
} as const;

export async function handoff(client: GosporeClient, req: systemTypes.BrowserCrawlHandoffReq, opts?: InvokeOptions): Promise<systemTypes.BrowserCrawlHandoffResp> {
  return client.invoke<systemTypes.BrowserCrawlHandoffReq, systemTypes.BrowserCrawlHandoffResp>("crawl.handoff", req, { reqSchemaId: 4247, resSchemaId: 4248, ...opts });
}

export const handoff_meta = {
  callable: "crawl.handoff",
  name: "handoff",
  reqSchemaId: 4247,
  resSchemaId: 4248,
} as const;

export async function list(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.ListResp> {
  return client.invoke<void, systemTypes.ListResp>("crawl.list", undefined, { resSchemaId: 1720, ...opts });
}

export async function loginDone(client: GosporeClient, req: systemTypes.LoginDoneReq, opts?: InvokeOptions): Promise<systemTypes.Task> {
  return client.invoke<systemTypes.LoginDoneReq, systemTypes.Task>("crawl.login_done", req, { reqSchemaId: 1779, resSchemaId: 139, ...opts });
}

export const loginDone_meta = {
  callable: "crawl.login_done",
  name: "login_done",
  reqSchemaId: 1779,
  resSchemaId: 139,
} as const;

export async function results(client: GosporeClient, req: systemTypes.BrowserCrawlResultsReq, opts?: InvokeOptions): Promise<systemTypes.BrowserCrawlResultsResp> {
  return client.invoke<systemTypes.BrowserCrawlResultsReq, systemTypes.BrowserCrawlResultsResp>("crawl.results", req, { reqSchemaId: 4244, resSchemaId: 4245, ...opts });
}

export const results_meta = {
  callable: "crawl.results",
  name: "results",
  reqSchemaId: 4244,
  resSchemaId: 4245,
} as const;

export async function start(client: GosporeClient, req: systemTypes.BrowserCrawlStartReq, opts?: InvokeOptions): Promise<systemTypes.BrowserCrawlStartResp> {
  return client.invoke<systemTypes.BrowserCrawlStartReq, systemTypes.BrowserCrawlStartResp>("crawl.start", req, { reqSchemaId: 4240, resSchemaId: 4241, ...opts });
}

export const start_meta = {
  callable: "crawl.start",
  name: "start",
  reqSchemaId: 4240,
  resSchemaId: 4241,
} as const;

export async function status(client: GosporeClient, req: systemTypes.BrowserCrawlStatusReq, opts?: InvokeOptions): Promise<systemTypes.BrowserCrawlStatusResp> {
  return client.invoke<systemTypes.BrowserCrawlStatusReq, systemTypes.BrowserCrawlStatusResp>("crawl.status", req, { reqSchemaId: 4242, resSchemaId: 4243, ...opts });
}

export const status_meta = {
  callable: "crawl.status",
  name: "status",
  reqSchemaId: 4242,
  resSchemaId: 4243,
} as const;

export async function submit(client: GosporeClient, req: systemTypes.SubmitReq, opts?: InvokeOptions): Promise<systemTypes.SubmitResp> {
  return client.invoke<systemTypes.SubmitReq, systemTypes.SubmitResp>("crawl.submit", req, { reqSchemaId: 1659, resSchemaId: 1660, ...opts });
}

export const submit_meta = {
  callable: "crawl.submit",
  name: "submit",
  reqSchemaId: 1659,
  resSchemaId: 1660,
} as const;

