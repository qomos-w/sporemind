// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "puppet.asset",
    name: "commit",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4554,
    finalSchemaId: 4555,
    req: {
      kind: "struct",
      name: "PuppetAssetCommitReq",
      className: "PuppetAssetCommitReq"
    },
    final: {
      kind: "struct",
      name: "PuppetAssetCommitResp",
      className: "PuppetAssetCommitResp"
    }
  },
  {
    namespace: "puppet.asset",
    name: "list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4550,
    finalSchemaId: 4551,
    req: {
      kind: "struct",
      name: "PuppetAssetListReq",
      className: "PuppetAssetListReq"
    },
    final: {
      kind: "struct",
      name: "PuppetAssetListResp",
      className: "PuppetAssetListResp"
    }
  },
  {
    namespace: "puppet.asset",
    name: "read",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4580,
    finalSchemaId: 4581,
    req: {
      kind: "struct",
      name: "PuppetAssetReadReq",
      className: "PuppetAssetReadReq"
    },
    final: {
      kind: "struct",
      name: "PuppetAssetReadResp",
      className: "PuppetAssetReadResp"
    }
  },
  {
    namespace: "puppet.asset",
    name: "reject",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4556,
    finalSchemaId: 4557,
    req: {
      kind: "struct",
      name: "PuppetAssetRejectReq",
      className: "PuppetAssetRejectReq"
    },
    final: {
      kind: "struct",
      name: "PuppetAssetRejectResp",
      className: "PuppetAssetRejectResp"
    }
  },
  {
    namespace: "puppet.asset",
    name: "stage",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4552,
    finalSchemaId: 4553,
    req: {
      kind: "struct",
      name: "PuppetAssetStageReq",
      className: "PuppetAssetStageReq"
    },
    final: {
      kind: "struct",
      name: "PuppetAssetStageResp",
      className: "PuppetAssetStageResp"
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
