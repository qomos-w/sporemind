// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function *bash(client: GosporeClient, req: systemTypes.ShellBashReq, opts?: InvokeOptions): AsyncIterable<systemTypes.ShellChunk> {
  yield* client.subscribe<systemTypes.ShellChunk>("shell.bash", req, { reqSchemaId: 1490, chunkSchemaId: 1492, ...opts });
}

export async function *exec(client: GosporeClient, req: systemTypes.ShellExecReq, opts?: InvokeOptions): AsyncIterable<systemTypes.ShellChunk> {
  yield* client.subscribe<systemTypes.ShellChunk>("shell.exec", req, { reqSchemaId: 1488, chunkSchemaId: 1492, ...opts });
}

export async function sessionClose(client: GosporeClient, req: systemTypes.ShellSessionCloseReq, opts?: InvokeOptions): Promise<systemTypes.ShellSessionCloseResp> {
  return client.invoke<systemTypes.ShellSessionCloseReq, systemTypes.ShellSessionCloseResp>("shell.session_close", req, { reqSchemaId: 5062, resSchemaId: 5063, ...opts });
}

export const sessionClose_meta = {
  callable: "shell.session_close",
  name: "session_close",
  reqSchemaId: 5062,
  resSchemaId: 5063,
} as const;

export async function sessionFetch(client: GosporeClient, req: systemTypes.ShellSessionFetchReq, opts?: InvokeOptions): Promise<systemTypes.ShellSessionFetchResp> {
  return client.invoke<systemTypes.ShellSessionFetchReq, systemTypes.ShellSessionFetchResp>("shell.session_fetch", req, { reqSchemaId: 5152, resSchemaId: 5153, ...opts });
}

export const sessionFetch_meta = {
  callable: "shell.session_fetch",
  name: "session_fetch",
  reqSchemaId: 5152,
  resSchemaId: 5153,
} as const;

export async function sessionOpen(client: GosporeClient, req: systemTypes.ShellSessionOpenReq, opts?: InvokeOptions): Promise<systemTypes.ShellSessionOpenResp> {
  return client.invoke<systemTypes.ShellSessionOpenReq, systemTypes.ShellSessionOpenResp>("shell.session_open", req, { reqSchemaId: 5056, resSchemaId: 5057, ...opts });
}

export const sessionOpen_meta = {
  callable: "shell.session_open",
  name: "session_open",
  reqSchemaId: 5056,
  resSchemaId: 5057,
} as const;

export async function sessionResize(client: GosporeClient, req: systemTypes.ShellSessionResizeReq, opts?: InvokeOptions): Promise<systemTypes.ShellSessionResizeResp> {
  return client.invoke<systemTypes.ShellSessionResizeReq, systemTypes.ShellSessionResizeResp>("shell.session_resize", req, { reqSchemaId: 5060, resSchemaId: 5061, ...opts });
}

export const sessionResize_meta = {
  callable: "shell.session_resize",
  name: "session_resize",
  reqSchemaId: 5060,
  resSchemaId: 5061,
} as const;

export async function sessionWrite(client: GosporeClient, req: systemTypes.ShellSessionWriteReq, opts?: InvokeOptions): Promise<systemTypes.ShellSessionWriteResp> {
  return client.invoke<systemTypes.ShellSessionWriteReq, systemTypes.ShellSessionWriteResp>("shell.session_write", req, { reqSchemaId: 5058, resSchemaId: 5059, ...opts });
}

export const sessionWrite_meta = {
  callable: "shell.session_write",
  name: "session_write",
  reqSchemaId: 5058,
  resSchemaId: 5059,
} as const;

export type ShellSessionOutputHandler = (payload: systemTypes.ShellSessionOutputEvent) => void;

export function OnShellSessionOutput(client: GosporeClient, handler: ShellSessionOutputHandler): () => void {
  return client.events.onService("shell", "shell.session_output", (payload) => handler(payload as systemTypes.ShellSessionOutputEvent));
}

export function OffShellSessionOutput(cancel: () => void): void {
  cancel();
}

