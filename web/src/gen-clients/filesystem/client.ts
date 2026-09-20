// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function edit(client: GosporeClient, req: systemTypes.FileSystemEditReq, opts?: InvokeOptions): Promise<systemTypes.FileSystemEditResp> {
  return client.invoke<systemTypes.FileSystemEditReq, systemTypes.FileSystemEditResp>("filesystem.edit", req, { reqSchemaId: 730, resSchemaId: 732, ...opts });
}

export const edit_meta = {
  callable: "filesystem.edit",
  name: "edit",
  reqSchemaId: 730,
  resSchemaId: 732,
} as const;

export async function glob(client: GosporeClient, req: systemTypes.FileSystemGlobReq, opts?: InvokeOptions): Promise<systemTypes.FileSystemGlobResp> {
  return client.invoke<systemTypes.FileSystemGlobReq, systemTypes.FileSystemGlobResp>("filesystem.glob", req, { reqSchemaId: 733, resSchemaId: 734, ...opts });
}

export const glob_meta = {
  callable: "filesystem.glob",
  name: "glob",
  reqSchemaId: 733,
  resSchemaId: 734,
} as const;

export async function grep(client: GosporeClient, req: systemTypes.FileSystemGrepReq, opts?: InvokeOptions): Promise<systemTypes.FileSystemGrepResp> {
  return client.invoke<systemTypes.FileSystemGrepReq, systemTypes.FileSystemGrepResp>("filesystem.grep", req, { reqSchemaId: 735, resSchemaId: 738, ...opts });
}

export const grep_meta = {
  callable: "filesystem.grep",
  name: "grep",
  reqSchemaId: 735,
  resSchemaId: 738,
} as const;

export async function list(client: GosporeClient, req: systemTypes.FileSystemListReq, opts?: InvokeOptions): Promise<string> {
  return client.invoke<systemTypes.FileSystemListReq, string>("filesystem.list", req, { reqSchemaId: 722, ...opts });
}

export const list_meta = {
  callable: "filesystem.list",
  name: "list",
  reqSchemaId: 722,
} as const;

export async function listJson(client: GosporeClient, req: systemTypes.FileSystemListReq, opts?: InvokeOptions): Promise<systemTypes.FileEntryListResp> {
  return client.invoke<systemTypes.FileSystemListReq, systemTypes.FileEntryListResp>("filesystem.list_json", req, { reqSchemaId: 722, resSchemaId: 721, ...opts });
}

export const listJson_meta = {
  callable: "filesystem.list_json",
  name: "list_json",
  reqSchemaId: 722,
  resSchemaId: 721,
} as const;

export async function read(client: GosporeClient, req: systemTypes.FileSystemReadReq, opts?: InvokeOptions): Promise<systemTypes.FileSystemReadResp> {
  return client.invoke<systemTypes.FileSystemReadReq, systemTypes.FileSystemReadResp>("filesystem.read", req, { reqSchemaId: 723, resSchemaId: 724, ...opts });
}

export const read_meta = {
  callable: "filesystem.read",
  name: "read",
  reqSchemaId: 723,
  resSchemaId: 724,
} as const;

export async function readBase64(client: GosporeClient, req: systemTypes.FileSystemReadBase64Req, opts?: InvokeOptions): Promise<systemTypes.FileSystemReadBase64Resp> {
  return client.invoke<systemTypes.FileSystemReadBase64Req, systemTypes.FileSystemReadBase64Resp>("filesystem.read_base64", req, { reqSchemaId: 725, resSchemaId: 746, ...opts });
}

export const readBase64_meta = {
  callable: "filesystem.read_base64",
  name: "read_base64",
  reqSchemaId: 725,
  resSchemaId: 746,
} as const;

export async function readChunk(client: GosporeClient, req: systemTypes.FileSystemReadChunkReq, opts?: InvokeOptions): Promise<systemTypes.FileSystemReadChunkResp> {
  return client.invoke<systemTypes.FileSystemReadChunkReq, systemTypes.FileSystemReadChunkResp>("filesystem.read_chunk", req, { reqSchemaId: 726, resSchemaId: 727, ...opts });
}

export const readChunk_meta = {
  callable: "filesystem.read_chunk",
  name: "read_chunk",
  reqSchemaId: 726,
  resSchemaId: 727,
} as const;

export async function rm(client: GosporeClient, req: systemTypes.FileSystemRmReq, opts?: InvokeOptions): Promise<systemTypes.FileSystemRmResp> {
  return client.invoke<systemTypes.FileSystemRmReq, systemTypes.FileSystemRmResp>("filesystem.rm", req, { reqSchemaId: 739, resSchemaId: 740, ...opts });
}

export const rm_meta = {
  callable: "filesystem.rm",
  name: "rm",
  reqSchemaId: 739,
  resSchemaId: 740,
} as const;

export async function roots(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.FileSystemRootsResp> {
  return client.invoke<void, systemTypes.FileSystemRootsResp>("filesystem.roots", undefined, { resSchemaId: 748, ...opts });
}

export async function write(client: GosporeClient, req: systemTypes.FileSystemWriteReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.FileSystemWriteReq, void>("filesystem.write", req, { reqSchemaId: 728, ...opts });
}

export const write_meta = {
  callable: "filesystem.write",
  name: "write",
  reqSchemaId: 728,
} as const;

export async function writeBase64(client: GosporeClient, req: systemTypes.FileSystemWriteBase64Req, opts?: InvokeOptions): Promise<systemTypes.FileSystemWriteResp> {
  return client.invoke<systemTypes.FileSystemWriteBase64Req, systemTypes.FileSystemWriteResp>("filesystem.write_base64", req, { reqSchemaId: 747, resSchemaId: 729, ...opts });
}

export const writeBase64_meta = {
  callable: "filesystem.write_base64",
  name: "write_base64",
  reqSchemaId: 747,
  resSchemaId: 729,
} as const;

