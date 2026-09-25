// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "runtime",
    name: "build_info",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 914,
    finalSchemaId: 915,
    req: {
      kind: "struct",
      name: "RuntimeBuildInfoReq",
      className: "RuntimeBuildInfoReq"
    },
    final: {
      kind: "struct",
      name: "RuntimeBuildInfoResp",
      className: "RuntimeBuildInfoResp"
    }
  },
  {
    namespace: "runtime",
    name: "list_services",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 912,
    finalSchemaId: 913,
    req: {
      kind: "struct",
      name: "RuntimeListServicesReq",
      className: "RuntimeListServicesReq"
    },
    final: {
      kind: "struct",
      name: "RuntimeListServicesResp",
      className: "RuntimeListServicesResp"
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
