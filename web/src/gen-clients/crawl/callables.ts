// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "crawl",
    name: "cancel",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1721,
    finalSchemaId: 139,
    req: {
      kind: "struct",
      name: "CancelReq",
      className: "CancelReq"
    },
    final: {
      kind: "struct",
      name: "Task",
      className: "Task"
    }
  },
  {
    namespace: "crawl",
    name: "get",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1801,
    finalSchemaId: 139,
    req: {
      kind: "struct",
      name: "GetReq",
      className: "GetReq"
    },
    final: {
      kind: "struct",
      name: "Task",
      className: "Task"
    }
  },
  {
    namespace: "crawl",
    name: "handoff",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 4375,
    finalSchemaId: 4376,
    req: {
      kind: "struct",
      name: "BrowserCrawlHandoffReq",
      className: "BrowserCrawlHandoffReq"
    },
    final: {
      kind: "struct",
      name: "BrowserCrawlHandoffResp",
      className: "BrowserCrawlHandoffResp"
    }
  },
  {
    namespace: "crawl",
    name: "list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 1720,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "ListResp",
      className: "ListResp"
    }
  },
  {
    namespace: "crawl",
    name: "login_done",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1779,
    finalSchemaId: 139,
    req: {
      kind: "struct",
      name: "LoginDoneReq",
      className: "LoginDoneReq"
    },
    final: {
      kind: "struct",
      name: "Task",
      className: "Task"
    }
  },
  {
    namespace: "crawl",
    name: "results",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4372,
    finalSchemaId: 4373,
    req: {
      kind: "struct",
      name: "BrowserCrawlResultsReq",
      className: "BrowserCrawlResultsReq"
    },
    final: {
      kind: "struct",
      name: "BrowserCrawlResultsResp",
      className: "BrowserCrawlResultsResp"
    }
  },
  {
    namespace: "crawl",
    name: "start",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4368,
    finalSchemaId: 4369,
    req: {
      kind: "struct",
      name: "BrowserCrawlStartReq",
      className: "BrowserCrawlStartReq"
    },
    final: {
      kind: "struct",
      name: "BrowserCrawlStartResp",
      className: "BrowserCrawlStartResp"
    }
  },
  {
    namespace: "crawl",
    name: "status",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4370,
    finalSchemaId: 4371,
    req: {
      kind: "struct",
      name: "BrowserCrawlStatusReq",
      className: "BrowserCrawlStatusReq"
    },
    final: {
      kind: "struct",
      name: "BrowserCrawlStatusResp",
      className: "BrowserCrawlStatusResp"
    }
  },
  {
    namespace: "crawl",
    name: "submit",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1799,
    finalSchemaId: 1800,
    req: {
      kind: "struct",
      name: "SubmitReq",
      className: "SubmitReq"
    },
    final: {
      kind: "struct",
      name: "SubmitResp",
      className: "SubmitResp"
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
