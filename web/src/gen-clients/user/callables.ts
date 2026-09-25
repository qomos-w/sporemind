// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "user",
    name: "auth_login",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1747,
    finalSchemaId: 1748,
    req: {
      kind: "struct",
      name: "AuthLoginReq",
      className: "AuthLoginReq"
    },
    final: {
      kind: "struct",
      name: "AuthLoginResp",
      className: "AuthLoginResp"
    }
  },
  {
    namespace: "user",
    name: "auth_me",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 1745,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "AccountView",
      className: "AccountView"
    }
  },
  {
    namespace: "user",
    name: "auth_refresh",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1749,
    finalSchemaId: 1750,
    req: {
      kind: "struct",
      name: "AuthRefreshReq",
      className: "AuthRefreshReq"
    },
    final: {
      kind: "struct",
      name: "AuthRefreshResp",
      className: "AuthRefreshResp"
    }
  },
  {
    namespace: "user",
    name: "auth_register",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1746,
    finalSchemaId: 1745,
    req: {
      kind: "struct",
      name: "AuthRegisterReq",
      className: "AuthRegisterReq"
    },
    final: {
      kind: "struct",
      name: "AccountView",
      className: "AccountView"
    }
  },
  {
    namespace: "user",
    name: "create",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1753,
    finalSchemaId: 1745,
    req: {
      kind: "struct",
      name: "AccountCreateReq",
      className: "AccountCreateReq"
    },
    final: {
      kind: "struct",
      name: "AccountView",
      className: "AccountView"
    }
  },
  {
    namespace: "user",
    name: "group_create",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1759,
    finalSchemaId: 1757,
    req: {
      kind: "struct",
      name: "GroupCreateReq",
      className: "GroupCreateReq"
    },
    final: {
      kind: "struct",
      name: "Group",
      className: "Group"
    }
  },
  {
    namespace: "user",
    name: "group_list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 1758,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "GroupListResp",
      className: "GroupListResp"
    }
  },
  {
    namespace: "user",
    name: "group_remove",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1761,
    finalSchemaId: 1757,
    req: {
      kind: "struct",
      name: "GroupDeleteReq",
      className: "GroupDeleteReq"
    },
    final: {
      kind: "struct",
      name: "Group",
      className: "Group"
    }
  },
  {
    namespace: "user",
    name: "group_update",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1760,
    finalSchemaId: 1757,
    req: {
      kind: "struct",
      name: "GroupUpdateReq",
      className: "GroupUpdateReq"
    },
    final: {
      kind: "struct",
      name: "Group",
      className: "Group"
    }
  },
  {
    namespace: "user",
    name: "list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 1752,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "AccountListResp",
      className: "AccountListResp"
    }
  },
  {
    namespace: "user",
    name: "permission_get",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 1763,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "PermissionMatrix",
      className: "PermissionMatrix"
    }
  },
  {
    namespace: "user",
    name: "permission_update",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1764,
    finalSchemaId: 1763,
    req: {
      kind: "struct",
      name: "PermissionUpdateReq",
      className: "PermissionUpdateReq"
    },
    final: {
      kind: "struct",
      name: "PermissionMatrix",
      className: "PermissionMatrix"
    }
  },
  {
    namespace: "user",
    name: "remove",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1755,
    finalSchemaId: 1745,
    req: {
      kind: "struct",
      name: "AccountDeleteReq",
      className: "AccountDeleteReq"
    },
    final: {
      kind: "struct",
      name: "AccountView",
      className: "AccountView"
    }
  },
  {
    namespace: "user",
    name: "reset_password",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1756,
    finalSchemaId: 1745,
    req: {
      kind: "struct",
      name: "AccountResetPasswordReq",
      className: "AccountResetPasswordReq"
    },
    final: {
      kind: "struct",
      name: "AccountView",
      className: "AccountView"
    }
  },
  {
    namespace: "user",
    name: "update",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1754,
    finalSchemaId: 1745,
    req: {
      kind: "struct",
      name: "AccountUpdateReq",
      className: "AccountUpdateReq"
    },
    final: {
      kind: "struct",
      name: "AccountView",
      className: "AccountView"
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
