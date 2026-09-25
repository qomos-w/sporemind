// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "im.route",
    name: "delete",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 4847,
    finalSchemaId: 4848,
    req: {
      kind: "struct",
      name: "ImRouteDeleteReq",
      className: "ImRouteDeleteReq"
    },
    final: {
      kind: "struct",
      name: "ImRouteDeleteResp",
      className: "ImRouteDeleteResp"
    }
  },
  {
    namespace: "im.route",
    name: "list",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 4843,
    finalSchemaId: 4844,
    req: {
      kind: "struct",
      name: "ImRouteListReq",
      className: "ImRouteListReq"
    },
    final: {
      kind: "struct",
      name: "ImRouteListResp",
      className: "ImRouteListResp"
    }
  },
  {
    namespace: "im.route",
    name: "set",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 4845,
    finalSchemaId: 4846,
    req: {
      kind: "struct",
      name: "ImRouteSetReq",
      className: "ImRouteSetReq"
    },
    final: {
      kind: "struct",
      name: "ImRouteSetResp",
      className: "ImRouteSetResp"
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
