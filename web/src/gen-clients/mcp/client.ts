// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function addServer(client: GosporeClient, req: systemTypes.McpAddServerReq, opts?: InvokeOptions): Promise<systemTypes.McpAddServerResp> {
  return client.invoke<systemTypes.McpAddServerReq, systemTypes.McpAddServerResp>("mcp.add_server", req, { reqSchemaId: 4314, resSchemaId: 4315, ...opts });
}

export const addServer_meta = {
  callable: "mcp.add_server",
  name: "add_server",
  reqSchemaId: 4314,
  resSchemaId: 4315,
} as const;

export async function callTool(client: GosporeClient, req: systemTypes.McpCallToolReq, opts?: InvokeOptions): Promise<systemTypes.McpCallToolResp> {
  return client.invoke<systemTypes.McpCallToolReq, systemTypes.McpCallToolResp>("mcp.call_tool", req, { reqSchemaId: 4327, resSchemaId: 4328, ...opts });
}

export const callTool_meta = {
  callable: "mcp.call_tool",
  name: "call_tool",
  reqSchemaId: 4327,
  resSchemaId: 4328,
} as const;

export async function connect(client: GosporeClient, req: systemTypes.McpConnectReq, opts?: InvokeOptions): Promise<systemTypes.McpConnectResp> {
  return client.invoke<systemTypes.McpConnectReq, systemTypes.McpConnectResp>("mcp.connect", req, { reqSchemaId: 4320, resSchemaId: 4321, ...opts });
}

export const connect_meta = {
  callable: "mcp.connect",
  name: "connect",
  reqSchemaId: 4320,
  resSchemaId: 4321,
} as const;

export async function disconnect(client: GosporeClient, req: systemTypes.McpDisconnectReq, opts?: InvokeOptions): Promise<systemTypes.McpDisconnectResp> {
  return client.invoke<systemTypes.McpDisconnectReq, systemTypes.McpDisconnectResp>("mcp.disconnect", req, { reqSchemaId: 4322, resSchemaId: 4323, ...opts });
}

export const disconnect_meta = {
  callable: "mcp.disconnect",
  name: "disconnect",
  reqSchemaId: 4322,
  resSchemaId: 4323,
} as const;

export async function discoverTools(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.McpDiscoverToolsResp> {
  return client.invoke<void, systemTypes.McpDiscoverToolsResp>("mcp.discover_tools", undefined, { resSchemaId: 4333, ...opts });
}

export async function listServers(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.McpListServersResp> {
  return client.invoke<void, systemTypes.McpListServersResp>("mcp.list_servers", undefined, { resSchemaId: 4313, ...opts });
}

export async function reconnect(client: GosporeClient, req: systemTypes.McpReconnectReq, opts?: InvokeOptions): Promise<systemTypes.McpReconnectResp> {
  return client.invoke<systemTypes.McpReconnectReq, systemTypes.McpReconnectResp>("mcp.reconnect", req, { reqSchemaId: 4324, resSchemaId: 4325, ...opts });
}

export const reconnect_meta = {
  callable: "mcp.reconnect",
  name: "reconnect",
  reqSchemaId: 4324,
  resSchemaId: 4325,
} as const;

export async function removeServer(client: GosporeClient, req: systemTypes.McpRemoveServerReq, opts?: InvokeOptions): Promise<systemTypes.McpRemoveServerResp> {
  return client.invoke<systemTypes.McpRemoveServerReq, systemTypes.McpRemoveServerResp>("mcp.remove_server", req, { reqSchemaId: 4318, resSchemaId: 4319, ...opts });
}

export const removeServer_meta = {
  callable: "mcp.remove_server",
  name: "remove_server",
  reqSchemaId: 4318,
  resSchemaId: 4319,
} as const;

export async function updateServer(client: GosporeClient, req: systemTypes.McpUpdateServerReq, opts?: InvokeOptions): Promise<systemTypes.McpUpdateServerResp> {
  return client.invoke<systemTypes.McpUpdateServerReq, systemTypes.McpUpdateServerResp>("mcp.update_server", req, { reqSchemaId: 4316, resSchemaId: 4317, ...opts });
}

export const updateServer_meta = {
  callable: "mcp.update_server",
  name: "update_server",
  reqSchemaId: 4316,
  resSchemaId: 4317,
} as const;

