// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "puppet.viewport",
    name: "capture",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4737,
    finalSchemaId: 4738,
    req: {
      kind: "struct",
      name: "PuppetViewportCaptureReq",
      className: "PuppetViewportCaptureReq"
    },
    final: {
      kind: "struct",
      name: "PuppetViewportCaptureResp",
      className: "PuppetViewportCaptureResp"
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
