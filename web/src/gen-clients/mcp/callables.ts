// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "mcp",
    name: "add_server",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 4314,
    finalSchemaId: 4315,
    req: {
      kind: "struct",
      name: "McpAddServerReq",
      className: "McpAddServerReq"
    },
    final: {
      kind: "struct",
      name: "McpAddServerResp",
      className: "McpAddServerResp"
    }
  },
  {
    namespace: "mcp",
    name: "call_tool",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 4327,
    finalSchemaId: 4328,
    req: {
      kind: "struct",
      name: "McpCallToolReq",
      className: "McpCallToolReq"
    },
    final: {
      kind: "struct",
      name: "McpCallToolResp",
      className: "McpCallToolResp"
    }
  },
  {
    namespace: "mcp",
    name: "connect",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 4320,
    finalSchemaId: 4321,
    req: {
      kind: "struct",
      name: "McpConnectReq",
      className: "McpConnectReq"
    },
    final: {
      kind: "struct",
      name: "McpConnectResp",
      className: "McpConnectResp"
    }
  },
  {
    namespace: "mcp",
    name: "disconnect",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 4322,
    finalSchemaId: 4323,
    req: {
      kind: "struct",
      name: "McpDisconnectReq",
      className: "McpDisconnectReq"
    },
    final: {
      kind: "struct",
      name: "McpDisconnectResp",
      className: "McpDisconnectResp"
    }
  },
  {
    namespace: "mcp",
    name: "discover_tools",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 4333,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "McpDiscoverToolsResp",
      className: "McpDiscoverToolsResp"
    }
  },
  {
    namespace: "mcp",
    name: "list_servers",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 4313,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "McpListServersResp",
      className: "McpListServersResp"
    }
  },
  {
    namespace: "mcp",
    name: "reconnect",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 4324,
    finalSchemaId: 4325,
    req: {
      kind: "struct",
      name: "McpReconnectReq",
      className: "McpReconnectReq"
    },
    final: {
      kind: "struct",
      name: "McpReconnectResp",
      className: "McpReconnectResp"
    }
  },
  {
    namespace: "mcp",
    name: "remove_server",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 4318,
    finalSchemaId: 4319,
    req: {
      kind: "struct",
      name: "McpRemoveServerReq",
      className: "McpRemoveServerReq"
    },
    final: {
      kind: "struct",
      name: "McpRemoveServerResp",
      className: "McpRemoveServerResp"
    }
  },
  {
    namespace: "mcp",
    name: "update_server",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 4316,
    finalSchemaId: 4317,
    req: {
      kind: "struct",
      name: "McpUpdateServerReq",
      className: "McpUpdateServerReq"
    },
    final: {
      kind: "struct",
      name: "McpUpdateServerResp",
      className: "McpUpdateServerResp"
    }
  },
];

export function buildCallableRegistry(): CallableRegistry {
  const registry = new CallableRegistry();
  for (const entry of callableEntries) {
    registry.register(entry);
  }
  return registry;
}
