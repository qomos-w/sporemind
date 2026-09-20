// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "puppet.param",
    name: "list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4588,
    finalSchemaId: 4589,
    req: {
      kind: "struct",
      name: "PuppetParamListReq",
      className: "PuppetParamListReq"
    },
    final: {
      kind: "struct",
      name: "PuppetParamListResp",
      className: "PuppetParamListResp"
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
