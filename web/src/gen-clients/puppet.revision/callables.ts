// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "puppet.revision",
    name: "log",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4703,
    finalSchemaId: 4704,
    req: {
      kind: "struct",
      name: "PuppetRevisionLogReq",
      className: "PuppetRevisionLogReq"
    },
    final: {
      kind: "struct",
      name: "PuppetRevisionLogResp",
      className: "PuppetRevisionLogResp"
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
