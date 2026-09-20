// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "workbench",
    name: "attention_report",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 6497,
    finalSchemaId: 6498,
    req: {
      kind: "struct",
      name: "WorkbenchAttentionReportReq",
      className: "WorkbenchAttentionReportReq"
    },
    final: {
      kind: "struct",
      name: "WorkbenchAttentionReportResp",
      className: "WorkbenchAttentionReportResp"
    }
  },
  {
    namespace: "workbench",
    name: "promote",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 6403,
    finalSchemaId: 6401,
    req: {
      kind: "struct",
      name: "WorkbenchCardRefReq",
      className: "WorkbenchCardRefReq"
    },
    final: {
      kind: "struct",
      name: "WorkbenchSnapshot",
      className: "WorkbenchSnapshot"
    }
  },
  {
    namespace: "workbench",
    name: "set_frozen",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 6407,
    finalSchemaId: 6401,
    req: {
      kind: "struct",
      name: "WorkbenchSetFrozenReq",
      className: "WorkbenchSetFrozenReq"
    },
    final: {
      kind: "struct",
      name: "WorkbenchSnapshot",
      className: "WorkbenchSnapshot"
    }
  },
  {
    namespace: "workbench",
    name: "set_hidden",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 6404,
    finalSchemaId: 6401,
    req: {
      kind: "struct",
      name: "WorkbenchSetHiddenReq",
      className: "WorkbenchSetHiddenReq"
    },
    final: {
      kind: "struct",
      name: "WorkbenchSnapshot",
      className: "WorkbenchSnapshot"
    }
  },
  {
    namespace: "workbench",
    name: "set_maximized",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 6408,
    finalSchemaId: 6401,
    req: {
      kind: "struct",
      name: "WorkbenchSetMaximizedReq",
      className: "WorkbenchSetMaximizedReq"
    },
    final: {
      kind: "struct",
      name: "WorkbenchSnapshot",
      className: "WorkbenchSnapshot"
    }
  },
  {
    namespace: "workbench",
    name: "set_pinned",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 6405,
    finalSchemaId: 6401,
    req: {
      kind: "struct",
      name: "WorkbenchSetPinnedReq",
      className: "WorkbenchSetPinnedReq"
    },
    final: {
      kind: "struct",
      name: "WorkbenchSnapshot",
      className: "WorkbenchSnapshot"
    }
  },
  {
    namespace: "workbench",
    name: "snapshot",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 6402,
    finalSchemaId: 6401,
    req: {
      kind: "struct",
      name: "WorkbenchSnapshotReq",
      className: "WorkbenchSnapshotReq"
    },
    final: {
      kind: "struct",
      name: "WorkbenchSnapshot",
      className: "WorkbenchSnapshot"
    }
  },
  {
    namespace: "workbench",
    name: "upsert_card",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 6406,
    finalSchemaId: 6401,
    req: {
      kind: "struct",
      name: "WorkbenchUpsertCardReq",
      className: "WorkbenchUpsertCardReq"
    },
    final: {
      kind: "struct",
      name: "WorkbenchSnapshot",
      className: "WorkbenchSnapshot"
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
