// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function close(client: GosporeClient, req: systemTypes.DbCloseReq, opts?: InvokeOptions): Promise<systemTypes.DbCloseResp> {
  return client.invoke<systemTypes.DbCloseReq, systemTypes.DbCloseResp>("dbclient.close", req, { reqSchemaId: 6264, resSchemaId: 6265, ...opts });
}

export const close_meta = {
  callable: "dbclient.close",
  name: "close",
  reqSchemaId: 6264,
  resSchemaId: 6265,
} as const;

export async function describe(client: GosporeClient, req: systemTypes.DbDescribeReq, opts?: InvokeOptions): Promise<systemTypes.DbDescribeResp> {
  return client.invoke<systemTypes.DbDescribeReq, systemTypes.DbDescribeResp>("dbclient.describe", req, { reqSchemaId: 6268, resSchemaId: 6269, ...opts });
}

export const describe_meta = {
  callable: "dbclient.describe",
  name: "describe",
  reqSchemaId: 6268,
  resSchemaId: 6269,
} as const;

export async function dialTest(client: GosporeClient, req: systemTypes.DbDialTestReq, opts?: InvokeOptions): Promise<systemTypes.DbDialTestResp> {
  return client.invoke<systemTypes.DbDialTestReq, systemTypes.DbDialTestResp>("dbclient.dial_test", req, { reqSchemaId: 6258, resSchemaId: 6259, ...opts });
}

export const dialTest_meta = {
  callable: "dbclient.dial_test",
  name: "dial_test",
  reqSchemaId: 6258,
  resSchemaId: 6259,
} as const;

export async function objectDelete(client: GosporeClient, req: systemTypes.DbObjectDeleteReq, opts?: InvokeOptions): Promise<systemTypes.DbObjectDeleteResp> {
  return client.invoke<systemTypes.DbObjectDeleteReq, systemTypes.DbObjectDeleteResp>("dbclient.object_delete", req, { reqSchemaId: 6277, resSchemaId: 6278, ...opts });
}

export const objectDelete_meta = {
  callable: "dbclient.object_delete",
  name: "object_delete",
  reqSchemaId: 6277,
  resSchemaId: 6278,
} as const;

export async function objectList(client: GosporeClient, req: systemTypes.DbObjectListReq, opts?: InvokeOptions): Promise<systemTypes.DbObjectListResp> {
  return client.invoke<systemTypes.DbObjectListReq, systemTypes.DbObjectListResp>("dbclient.object_list", req, { reqSchemaId: 6271, resSchemaId: 6272, ...opts });
}

export const objectList_meta = {
  callable: "dbclient.object_list",
  name: "object_list",
  reqSchemaId: 6271,
  resSchemaId: 6272,
} as const;

export async function objectMkdir(client: GosporeClient, req: systemTypes.DbObjectMkdirReq, opts?: InvokeOptions): Promise<systemTypes.DbObjectMkdirResp> {
  return client.invoke<systemTypes.DbObjectMkdirReq, systemTypes.DbObjectMkdirResp>("dbclient.object_mkdir", req, { reqSchemaId: 6279, resSchemaId: 6280, ...opts });
}

export const objectMkdir_meta = {
  callable: "dbclient.object_mkdir",
  name: "object_mkdir",
  reqSchemaId: 6279,
  resSchemaId: 6280,
} as const;

export async function objectRead(client: GosporeClient, req: systemTypes.DbObjectReadReq, opts?: InvokeOptions): Promise<systemTypes.DbObjectReadResp> {
  return client.invoke<systemTypes.DbObjectReadReq, systemTypes.DbObjectReadResp>("dbclient.object_read", req, { reqSchemaId: 6273, resSchemaId: 6274, ...opts });
}

export const objectRead_meta = {
  callable: "dbclient.object_read",
  name: "object_read",
  reqSchemaId: 6273,
  resSchemaId: 6274,
} as const;

export async function objectStat(client: GosporeClient, req: systemTypes.DbObjectStatReq, opts?: InvokeOptions): Promise<systemTypes.DbObjectStatResp> {
  return client.invoke<systemTypes.DbObjectStatReq, systemTypes.DbObjectStatResp>("dbclient.object_stat", req, { reqSchemaId: 6281, resSchemaId: 6282, ...opts });
}

export const objectStat_meta = {
  callable: "dbclient.object_stat",
  name: "object_stat",
  reqSchemaId: 6281,
  resSchemaId: 6282,
} as const;

export async function objectWrite(client: GosporeClient, req: systemTypes.DbObjectWriteReq, opts?: InvokeOptions): Promise<systemTypes.DbObjectWriteResp> {
  return client.invoke<systemTypes.DbObjectWriteReq, systemTypes.DbObjectWriteResp>("dbclient.object_write", req, { reqSchemaId: 6275, resSchemaId: 6276, ...opts });
}

export const objectWrite_meta = {
  callable: "dbclient.object_write",
  name: "object_write",
  reqSchemaId: 6275,
  resSchemaId: 6276,
} as const;

export async function query(client: GosporeClient, req: systemTypes.DbQueryReq, opts?: InvokeOptions): Promise<systemTypes.DbRows> {
  return client.invoke<systemTypes.DbQueryReq, systemTypes.DbRows>("dbclient.query", req, { reqSchemaId: 6263, resSchemaId: 6257, ...opts });
}

export const query_meta = {
  callable: "dbclient.query",
  name: "query",
  reqSchemaId: 6263,
  resSchemaId: 6257,
} as const;

export async function read(client: GosporeClient, req: systemTypes.DbReadReq, opts?: InvokeOptions): Promise<systemTypes.DbRows> {
  return client.invoke<systemTypes.DbReadReq, systemTypes.DbRows>("dbclient.read", req, { reqSchemaId: 6262, resSchemaId: 6257, ...opts });
}

export const read_meta = {
  callable: "dbclient.read",
  name: "read",
  reqSchemaId: 6262,
  resSchemaId: 6257,
} as const;

export async function tree(client: GosporeClient, req: systemTypes.DbTreeReq, opts?: InvokeOptions): Promise<systemTypes.DbTreeResp> {
  return client.invoke<systemTypes.DbTreeReq, systemTypes.DbTreeResp>("dbclient.tree", req, { reqSchemaId: 6260, resSchemaId: 6261, ...opts });
}

export const tree_meta = {
  callable: "dbclient.tree",
  name: "tree",
  reqSchemaId: 6260,
  resSchemaId: 6261,
} as const;

