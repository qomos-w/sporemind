// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export type McpServerStatusHandler = (payload: systemTypes.McpServerStatusEvent) => void;

export function OnMcpServerStatus(client: GosporeClient, handler: McpServerStatusHandler): () => void {
  return client.events.onService("mcp", "mcp.server_status", (payload) => handler(payload as systemTypes.McpServerStatusEvent));
}

export function OffMcpServerStatus(cancel: () => void): void {
  cancel();
}

