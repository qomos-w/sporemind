// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "puppet.asset",
    name: "commit",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4698,
    finalSchemaId: 4699,
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
    reqSchemaId: 4694,
    finalSchemaId: 4695,
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
    reqSchemaId: 4724,
    finalSchemaId: 4725,
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
    reqSchemaId: 4700,
    finalSchemaId: 4701,
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
    reqSchemaId: 4696,
    finalSchemaId: 4697,
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
