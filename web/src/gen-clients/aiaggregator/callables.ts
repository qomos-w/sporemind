// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "aiaggregator",
    name: "dispatch",
    visibility: "public",
    mode: "streaming",
    reqSchemaId: 347,
    chunkSchemaId: 369,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "SendSessionMessageReq",
      className: "SendSessionMessageReq"
    },
    chunk: {
      kind: "struct",
      name: "AggregatorChunk",
      className: "AggregatorChunk"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "aiaggregator",
    name: "intent",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 347,
    finalSchemaId: 348,
    req: {
      kind: "struct",
      name: "SendSessionMessageReq",
      className: "SendSessionMessageReq"
    },
    final: {
      kind: "struct",
      name: "SummarizeResp",
      className: "SummarizeResp"
    }
  },
  {
    namespace: "aiaggregator",
    name: "probe_tokens",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 347,
    finalSchemaId: 349,
    req: {
      kind: "struct",
      name: "SendSessionMessageReq",
      className: "SendSessionMessageReq"
    },
    final: {
      kind: "struct",
      name: "ProbeTokensResp",
      className: "ProbeTokensResp"
    }
  },
  {
    namespace: "aiaggregator",
    name: "status",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 353,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "AIAggregatorStatusResp",
      className: "AIAggregatorStatusResp"
    }
  },
  {
    namespace: "aiaggregator",
    name: "summarize",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 347,
    finalSchemaId: 348,
    req: {
      kind: "struct",
      name: "SendSessionMessageReq",
      className: "SendSessionMessageReq"
    },
    final: {
      kind: "struct",
      name: "SummarizeResp",
      className: "SummarizeResp"
    }
  },
  {
    namespace: "aiaggregator",
    name: "tool_judge",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 347,
    finalSchemaId: 348,
    req: {
      kind: "struct",
      name: "SendSessionMessageReq",
      className: "SendSessionMessageReq"
    },
    final: {
      kind: "struct",
      name: "SummarizeResp",
      className: "SummarizeResp"
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
