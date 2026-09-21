// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "mcp",
    name: "add_server",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 4186,
    finalSchemaId: 4187,
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
    reqSchemaId: 4199,
    finalSchemaId: 4200,
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
    reqSchemaId: 4192,
    finalSchemaId: 4193,
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
    reqSchemaId: 4194,
    finalSchemaId: 4195,
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
    finalSchemaId: 4205,
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
    finalSchemaId: 4185,
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
    reqSchemaId: 4196,
    finalSchemaId: 4197,
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
    reqSchemaId: 4190,
    finalSchemaId: 4191,
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
    reqSchemaId: 4188,
    finalSchemaId: 4189,
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
