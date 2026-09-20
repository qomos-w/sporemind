// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "websearch",
    name: "account_activate",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 4448,
    finalSchemaId: 4449,
    req: {
      kind: "struct",
      name: "WebSearchAccountActivateReq",
      className: "WebSearchAccountActivateReq"
    },
    final: {
      kind: "struct",
      name: "WebSearchAccountActivateResp",
      className: "WebSearchAccountActivateResp"
    }
  },
  {
    namespace: "websearch",
    name: "account_create",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 4442,
    finalSchemaId: 4443,
    req: {
      kind: "struct",
      name: "WebSearchAccountCreateReq",
      className: "WebSearchAccountCreateReq"
    },
    final: {
      kind: "struct",
      name: "WebSearchAccountCreateResp",
      className: "WebSearchAccountCreateResp"
    }
  },
  {
    namespace: "websearch",
    name: "account_delete",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 4446,
    finalSchemaId: 4447,
    req: {
      kind: "struct",
      name: "WebSearchAccountDeleteReq",
      className: "WebSearchAccountDeleteReq"
    },
    final: {
      kind: "struct",
      name: "WebSearchAccountDeleteResp",
      className: "WebSearchAccountDeleteResp"
    }
  },
  {
    namespace: "websearch",
    name: "account_list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4440,
    finalSchemaId: 4441,
    req: {
      kind: "struct",
      name: "WebSearchAccountListReq",
      className: "WebSearchAccountListReq"
    },
    final: {
      kind: "struct",
      name: "WebSearchAccountListResp",
      className: "WebSearchAccountListResp"
    }
  },
  {
    namespace: "websearch",
    name: "account_update",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 4444,
    finalSchemaId: 4445,
    req: {
      kind: "struct",
      name: "WebSearchAccountUpdateReq",
      className: "WebSearchAccountUpdateReq"
    },
    final: {
      kind: "struct",
      name: "WebSearchAccountUpdateResp",
      className: "WebSearchAccountUpdateResp"
    }
  },
  {
    namespace: "websearch",
    name: "download",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4453,
    finalSchemaId: 4454,
    req: {
      kind: "struct",
      name: "WebDownloadReq",
      className: "WebDownloadReq"
    },
    final: {
      kind: "struct",
      name: "WebDownloadResp",
      className: "WebDownloadResp"
    }
  },
  {
    namespace: "websearch",
    name: "fetch",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4450,
    finalSchemaId: 4452,
    req: {
      kind: "struct",
      name: "WebFetchReq",
      className: "WebFetchReq"
    },
    final: {
      kind: "struct",
      name: "WebFetchResp",
      className: "WebFetchResp"
    }
  },
  {
    namespace: "websearch",
    name: "provider_list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 4437,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "WebSearchProviderListResp",
      className: "WebSearchProviderListResp"
    }
  },
  {
    namespace: "websearch",
    name: "search",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4432,
    finalSchemaId: 4433,
    req: {
      kind: "struct",
      name: "WebSearchReq",
      className: "WebSearchReq"
    },
    final: {
      kind: "struct",
      name: "WebSearchResp",
      className: "WebSearchResp"
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
