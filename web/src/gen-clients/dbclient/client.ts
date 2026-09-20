// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function close(client: GosporeClient, req: systemTypes.DbCloseReq, opts?: InvokeOptions): Promise<systemTypes.DbCloseResp> {
  return client.invoke<systemTypes.DbCloseReq, systemTypes.DbCloseResp>("dbclient.close", req, { reqSchemaId: 6168, resSchemaId: 6169, ...opts });
}

export const close_meta = {
  callable: "dbclient.close",
  name: "close",
  reqSchemaId: 6168,
  resSchemaId: 6169,
} as const;

export async function describe(client: GosporeClient, req: systemTypes.DbDescribeReq, opts?: InvokeOptions): Promise<systemTypes.DbDescribeResp> {
  return client.invoke<systemTypes.DbDescribeReq, systemTypes.DbDescribeResp>("dbclient.describe", req, { reqSchemaId: 6172, resSchemaId: 6173, ...opts });
}

export const describe_meta = {
  callable: "dbclient.describe",
  name: "describe",
  reqSchemaId: 6172,
  resSchemaId: 6173,
} as const;

export async function dialTest(client: GosporeClient, req: systemTypes.DbDialTestReq, opts?: InvokeOptions): Promise<systemTypes.DbDialTestResp> {
  return client.invoke<systemTypes.DbDialTestReq, systemTypes.DbDialTestResp>("dbclient.dial_test", req, { reqSchemaId: 6162, resSchemaId: 6163, ...opts });
}

export const dialTest_meta = {
  callable: "dbclient.dial_test",
  name: "dial_test",
  reqSchemaId: 6162,
  resSchemaId: 6163,
} as const;

export async function objectDelete(client: GosporeClient, req: systemTypes.DbObjectDeleteReq, opts?: InvokeOptions): Promise<systemTypes.DbObjectDeleteResp> {
  return client.invoke<systemTypes.DbObjectDeleteReq, systemTypes.DbObjectDeleteResp>("dbclient.object_delete", req, { reqSchemaId: 6181, resSchemaId: 6182, ...opts });
}

export const objectDelete_meta = {
  callable: "dbclient.object_delete",
  name: "object_delete",
  reqSchemaId: 6181,
  resSchemaId: 6182,
} as const;

export async function objectList(client: GosporeClient, req: systemTypes.DbObjectListReq, opts?: InvokeOptions): Promise<systemTypes.DbObjectListResp> {
  return client.invoke<systemTypes.DbObjectListReq, systemTypes.DbObjectListResp>("dbclient.object_list", req, { reqSchemaId: 6175, resSchemaId: 6176, ...opts });
}

export const objectList_meta = {
  callable: "dbclient.object_list",
  name: "object_list",
  reqSchemaId: 6175,
  resSchemaId: 6176,
} as const;

export async function objectMkdir(client: GosporeClient, req: systemTypes.DbObjectMkdirReq, opts?: InvokeOptions): Promise<systemTypes.DbObjectMkdirResp> {
  return client.invoke<systemTypes.DbObjectMkdirReq, systemTypes.DbObjectMkdirResp>("dbclient.object_mkdir", req, { reqSchemaId: 6183, resSchemaId: 6184, ...opts });
}

export const objectMkdir_meta = {
  callable: "dbclient.object_mkdir",
  name: "object_mkdir",
  reqSchemaId: 6183,
  resSchemaId: 6184,
} as const;

export async function objectRead(client: GosporeClient, req: systemTypes.DbObjectReadReq, opts?: InvokeOptions): Promise<systemTypes.DbObjectReadResp> {
  return client.invoke<systemTypes.DbObjectReadReq, systemTypes.DbObjectReadResp>("dbclient.object_read", req, { reqSchemaId: 6177, resSchemaId: 6178, ...opts });
}

export const objectRead_meta = {
  callable: "dbclient.object_read",
  name: "object_read",
  reqSchemaId: 6177,
  resSchemaId: 6178,
} as const;

export async function objectStat(client: GosporeClient, req: systemTypes.DbObjectStatReq, opts?: InvokeOptions): Promise<systemTypes.DbObjectStatResp> {
  return client.invoke<systemTypes.DbObjectStatReq, systemTypes.DbObjectStatResp>("dbclient.object_stat", req, { reqSchemaId: 6185, resSchemaId: 6186, ...opts });
}

export const objectStat_meta = {
  callable: "dbclient.object_stat",
  name: "object_stat",
  reqSchemaId: 6185,
  resSchemaId: 6186,
} as const;

export async function objectWrite(client: GosporeClient, req: systemTypes.DbObjectWriteReq, opts?: InvokeOptions): Promise<systemTypes.DbObjectWriteResp> {
  return client.invoke<systemTypes.DbObjectWriteReq, systemTypes.DbObjectWriteResp>("dbclient.object_write", req, { reqSchemaId: 6179, resSchemaId: 6180, ...opts });
}

export const objectWrite_meta = {
  callable: "dbclient.object_write",
  name: "object_write",
  reqSchemaId: 6179,
  resSchemaId: 6180,
} as const;

export async function query(client: GosporeClient, req: systemTypes.DbQueryReq, opts?: InvokeOptions): Promise<systemTypes.DbRows> {
  return client.invoke<systemTypes.DbQueryReq, systemTypes.DbRows>("dbclient.query", req, { reqSchemaId: 6167, resSchemaId: 6161, ...opts });
}

export const query_meta = {
  callable: "dbclient.query",
  name: "query",
  reqSchemaId: 6167,
  resSchemaId: 6161,
} as const;

export async function read(client: GosporeClient, req: systemTypes.DbReadReq, opts?: InvokeOptions): Promise<systemTypes.DbRows> {
  return client.invoke<systemTypes.DbReadReq, systemTypes.DbRows>("dbclient.read", req, { reqSchemaId: 6166, resSchemaId: 6161, ...opts });
}

export const read_meta = {
  callable: "dbclient.read",
  name: "read",
  reqSchemaId: 6166,
  resSchemaId: 6161,
} as const;

export async function tree(client: GosporeClient, req: systemTypes.DbTreeReq, opts?: InvokeOptions): Promise<systemTypes.DbTreeResp> {
  return client.invoke<systemTypes.DbTreeReq, systemTypes.DbTreeResp>("dbclient.tree", req, { reqSchemaId: 6164, resSchemaId: 6165, ...opts });
}

export const tree_meta = {
  callable: "dbclient.tree",
  name: "tree",
  reqSchemaId: 6164,
  resSchemaId: 6165,
} as const;

