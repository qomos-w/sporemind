// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "cloudaccount",
    name: "content_detail",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4392,
    finalSchemaId: 4393,
    req: {
      kind: "struct",
      name: "ContentDetailReq",
      className: "ContentDetailReq"
    },
    final: {
      kind: "struct",
      name: "ContentDetailResp",
      className: "ContentDetailResp"
    }
  },
  {
    namespace: "cloudaccount",
    name: "content_install",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4394,
    finalSchemaId: 4395,
    req: {
      kind: "struct",
      name: "ContentInstallReq",
      className: "ContentInstallReq"
    },
    final: {
      kind: "struct",
      name: "ContentInstallResp",
      className: "ContentInstallResp"
    }
  },
  {
    namespace: "cloudaccount",
    name: "content_search",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4390,
    finalSchemaId: 4391,
    req: {
      kind: "struct",
      name: "ContentSearchReq",
      className: "ContentSearchReq"
    },
    final: {
      kind: "struct",
      name: "ContentSearchResp",
      className: "ContentSearchResp"
    }
  },
  {
    namespace: "cloudaccount",
    name: "get_entitlements",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 4388,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "CloudAccountGetEntitlementsResp",
      className: "CloudAccountGetEntitlementsResp"
    }
  },
  {
    namespace: "cloudaccount",
    name: "link",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4386,
    finalSchemaId: 4385,
    req: {
      kind: "struct",
      name: "CloudAccountLinkReq",
      className: "CloudAccountLinkReq"
    },
    final: {
      kind: "struct",
      name: "CloudAccountStatus",
      className: "CloudAccountStatus"
    }
  },
  {
    namespace: "cloudaccount",
    name: "redeem",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4398,
    finalSchemaId: 4399,
    req: {
      kind: "struct",
      name: "CdkeyRedeemReq",
      className: "CdkeyRedeemReq"
    },
    final: {
      kind: "struct",
      name: "CdkeyRedeemResp",
      className: "CdkeyRedeemResp"
    }
  },
  {
    namespace: "cloudaccount",
    name: "session_token",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 4401,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "CloudAccountSessionTokenResp",
      className: "CloudAccountSessionTokenResp"
    }
  },
  {
    namespace: "cloudaccount",
    name: "status",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 4385,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "CloudAccountStatus",
      className: "CloudAccountStatus"
    }
  },
  {
    namespace: "cloudaccount",
    name: "sync",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 4385,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "CloudAccountStatus",
      className: "CloudAccountStatus"
    }
  },
  {
    namespace: "cloudaccount",
    name: "unlink",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 4387,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "CloudAccountUnlinkResp",
      className: "CloudAccountUnlinkResp"
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
