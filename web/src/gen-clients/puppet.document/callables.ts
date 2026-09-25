// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "puppet.document",
    name: "revert_to",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4726,
    finalSchemaId: 4727,
    req: {
      kind: "struct",
      name: "PuppetDocumentRevertToReq",
      className: "PuppetDocumentRevertToReq"
    },
    final: {
      kind: "struct",
      name: "PuppetDocumentRevertToResp",
      className: "PuppetDocumentRevertToResp"
    }
  },
  {
    namespace: "puppet.document",
    name: "snapshot",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4691,
    finalSchemaId: 4692,
    req: {
      kind: "struct",
      name: "PuppetDocumentSnapshotReq",
      className: "PuppetDocumentSnapshotReq"
    },
    final: {
      kind: "struct",
      name: "PuppetDocumentSnapshotResp",
      className: "PuppetDocumentSnapshotResp"
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
