// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "appmanager",
    name: "agent_action",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2912,
    finalSchemaId: 2913,
    req: {
      kind: "struct",
      name: "AppManagerAgentActionReq",
      className: "AppManagerAgentActionReq"
    },
    final: {
      kind: "struct",
      name: "AppManagerAgentActionResp",
      className: "AppManagerAgentActionResp"
    }
  },
  {
    namespace: "appmanager",
    name: "app_export",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 6208,
    finalSchemaId: 6209,
    req: {
      kind: "struct",
      name: "AppManagerAppExportReq",
      className: "AppManagerAppExportReq"
    },
    final: {
      kind: "struct",
      name: "AppManagerAppExportResp",
      className: "AppManagerAppExportResp"
    }
  },
  {
    namespace: "appmanager",
    name: "audit",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 2189,
    finalSchemaId: 2190,
    req: {
      kind: "struct",
      name: "AppManagerAuditReq",
      className: "AppManagerAuditReq"
    },
    final: {
      kind: "struct",
      name: "AppManagerAuditResp",
      className: "AppManagerAuditResp"
    }
  },
  {
    namespace: "appmanager",
    name: "callable_info",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4784,
    finalSchemaId: 4787,
    req: {
      kind: "struct",
      name: "AppManagerCallableInfoReq",
      className: "AppManagerCallableInfoReq"
    },
    final: {
      kind: "struct",
      name: "AppManagerCallableInfoResp",
      className: "AppManagerCallableInfoResp"
    }
  },
  {
    namespace: "appmanager",
    name: "cast",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2184,
    finalSchemaId: 2185,
    req: {
      kind: "struct",
      name: "AppManagerCastReq",
      className: "AppManagerCastReq"
    },
    final: {
      kind: "struct",
      name: "AppManagerCastResp",
      className: "AppManagerCastResp"
    }
  },
  {
    namespace: "appmanager",
    name: "component_get",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5730,
    finalSchemaId: 5731,
    req: {
      kind: "struct",
      name: "AppManagerComponentGetReq",
      className: "AppManagerComponentGetReq"
    },
    final: {
      kind: "struct",
      name: "AppManagerComponentGetResp",
      className: "AppManagerComponentGetResp"
    }
  },
  {
    namespace: "appmanager",
    name: "component_list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5728,
    finalSchemaId: 5729,
    req: {
      kind: "struct",
      name: "AppManagerComponentListReq",
      className: "AppManagerComponentListReq"
    },
    final: {
      kind: "struct",
      name: "AppManagerComponentListResp",
      className: "AppManagerComponentListResp"
    }
  },
  {
    namespace: "appmanager",
    name: "dev_gate",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4944,
    finalSchemaId: 4947,
    req: {
      kind: "struct",
      name: "AppManagerDevGateReq",
      className: "AppManagerDevGateReq"
    },
    final: {
      kind: "struct",
      name: "AppManagerDevGateResp",
      className: "AppManagerDevGateResp"
    }
  },
  {
    namespace: "appmanager",
    name: "dev_generate",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4896,
    finalSchemaId: 4898,
    req: {
      kind: "struct",
      name: "AppManagerDevGenerateReq",
      className: "AppManagerDevGenerateReq"
    },
    final: {
      kind: "struct",
      name: "AppManagerDevGenerateResp",
      className: "AppManagerDevGenerateResp"
    }
  },
  {
    namespace: "appmanager",
    name: "dev_guide",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4788,
    finalSchemaId: 4791,
    req: {
      kind: "struct",
      name: "AppManagerDevGuideReq",
      className: "AppManagerDevGuideReq"
    },
    final: {
      kind: "struct",
      name: "AppManagerDevGuideResp",
      className: "AppManagerDevGuideResp"
    }
  },
  {
    namespace: "appmanager",
    name: "emit",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2186,
    finalSchemaId: 2187,
    req: {
      kind: "struct",
      name: "AppManagerEmitReq",
      className: "AppManagerEmitReq"
    },
    final: {
      kind: "struct",
      name: "AppManagerEmitResp",
      className: "AppManagerEmitResp"
    }
  },
  {
    namespace: "appmanager",
    name: "get",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2178,
    finalSchemaId: 2179,
    req: {
      kind: "struct",
      name: "AppManagerGetReq",
      className: "AppManagerGetReq"
    },
    final: {
      kind: "struct",
      name: "AppManagerGetResp",
      className: "AppManagerGetResp"
    }
  },
  {
    namespace: "appmanager",
    name: "host_protocol",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5872,
    finalSchemaId: 5875,
    req: {
      kind: "struct",
      name: "AppManagerHostProtocolReq",
      className: "AppManagerHostProtocolReq"
    },
    final: {
      kind: "struct",
      name: "AppManagerHostProtocolResp",
      className: "AppManagerHostProtocolResp"
    }
  },
  {
    namespace: "appmanager",
    name: "icon_names",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5632,
    finalSchemaId: 5634,
    req: {
      kind: "struct",
      name: "AppManagerIconNamesReq",
      className: "AppManagerIconNamesReq"
    },
    final: {
      kind: "struct",
      name: "AppManagerIconNamesResp",
      className: "AppManagerIconNamesResp"
    }
  },
  {
    namespace: "appmanager",
    name: "install_local",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 5584,
    finalSchemaId: 5585,
    req: {
      kind: "struct",
      name: "AppManagerInstallLocalReq",
      className: "AppManagerInstallLocalReq"
    },
    final: {
      kind: "struct",
      name: "AppManagerInstallLocalResp",
      className: "AppManagerInstallLocalResp"
    }
  },
  {
    namespace: "appmanager",
    name: "invoke",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2182,
    finalSchemaId: 2183,
    req: {
      kind: "struct",
      name: "AppManagerInvokeReq",
      className: "AppManagerInvokeReq"
    },
    final: {
      kind: "struct",
      name: "AppManagerInvokeResp",
      className: "AppManagerInvokeResp"
    }
  },
  {
    namespace: "appmanager",
    name: "invoke_stream",
    visibility: "public",
    mode: "streaming",
    reqSchemaId: 2182,
    chunkSchemaId: 2329,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "AppManagerInvokeReq",
      className: "AppManagerInvokeReq"
    },
    chunk: {
      kind: "struct",
      name: "PluginInvokeChunk",
      className: "PluginInvokeChunk"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "appmanager",
    name: "list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2180,
    finalSchemaId: 2181,
    req: {
      kind: "struct",
      name: "AppManagerListReq",
      className: "AppManagerListReq"
    },
    final: {
      kind: "struct",
      name: "AppManagerListResp",
      className: "AppManagerListResp"
    }
  },
  {
    namespace: "appmanager",
    name: "open_view",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5680,
    finalSchemaId: 5681,
    req: {
      kind: "struct",
      name: "AppManagerOpenViewReq",
      className: "AppManagerOpenViewReq"
    },
    final: {
      kind: "struct",
      name: "AppManagerOpenViewResp",
      className: "AppManagerOpenViewResp"
    }
  },
  {
    namespace: "appmanager",
    name: "panel_topology",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 6160,
    finalSchemaId: 6161,
    req: {
      kind: "struct",
      name: "AppManagerPanelTopologyReq",
      className: "AppManagerPanelTopologyReq"
    },
    final: {
      kind: "struct",
      name: "AppManagerPanelTopologyResp",
      className: "AppManagerPanelTopologyResp"
    }
  },
  {
    namespace: "appmanager",
    name: "plugin_emit",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 5920,
    finalSchemaId: 5921,
    req: {
      kind: "struct",
      name: "AppManagerPluginEmitReq",
      className: "AppManagerPluginEmitReq"
    },
    final: {
      kind: "struct",
      name: "AppManagerPluginEmitResp",
      className: "AppManagerPluginEmitResp"
    }
  },
  {
    namespace: "appmanager",
    name: "plugin_load",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5536,
    finalSchemaId: 5537,
    req: {
      kind: "struct",
      name: "AppManagerPluginLoadReq",
      className: "AppManagerPluginLoadReq"
    },
    final: {
      kind: "struct",
      name: "AppManagerPluginLoadResp",
      className: "AppManagerPluginLoadResp"
    }
  },
  {
    namespace: "appmanager",
    name: "plugin_unload",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5538,
    finalSchemaId: 5539,
    req: {
      kind: "struct",
      name: "AppManagerPluginUnloadReq",
      className: "AppManagerPluginUnloadReq"
    },
    final: {
      kind: "struct",
      name: "AppManagerPluginUnloadResp",
      className: "AppManagerPluginUnloadResp"
    }
  },
  {
    namespace: "appmanager",
    name: "project_package",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 3072,
    finalSchemaId: 3073,
    req: {
      kind: "struct",
      name: "AppManagerProjectPackageReq",
      className: "AppManagerProjectPackageReq"
    },
    final: {
      kind: "struct",
      name: "AppManagerProjectPackageResp",
      className: "AppManagerProjectPackageResp"
    }
  },
  {
    namespace: "appmanager",
    name: "register",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 2176,
    finalSchemaId: 2132,
    req: {
      kind: "struct",
      name: "AppManagerRegisterReq",
      className: "AppManagerRegisterReq"
    },
    final: {
      kind: "struct",
      name: "AppStatus",
      className: "AppStatus"
    }
  },
  {
    namespace: "appmanager",
    name: "register_project",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3120,
    finalSchemaId: 3121,
    req: {
      kind: "struct",
      name: "AppManagerRegisterProjectReq",
      className: "AppManagerRegisterProjectReq"
    },
    final: {
      kind: "struct",
      name: "AppManagerRegisterProjectResp",
      className: "AppManagerRegisterProjectResp"
    }
  },
  {
    namespace: "appmanager",
    name: "registration_preview",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 6112,
    finalSchemaId: 6113,
    req: {
      kind: "struct",
      name: "AppManagerRegistrationPreviewReq",
      className: "AppManagerRegistrationPreviewReq"
    },
    final: {
      kind: "struct",
      name: "AppManagerRegistrationPreviewResp",
      className: "AppManagerRegistrationPreviewResp"
    }
  },
  {
    namespace: "appmanager",
    name: "reload",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 2914,
    finalSchemaId: 2915,
    req: {
      kind: "struct",
      name: "AppManagerReloadReq",
      className: "AppManagerReloadReq"
    },
    final: {
      kind: "struct",
      name: "AppManagerReloadResp",
      className: "AppManagerReloadResp"
    }
  },
  {
    namespace: "appmanager",
    name: "reload_project",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 3122,
    finalSchemaId: 3123,
    req: {
      kind: "struct",
      name: "AppManagerReloadProjectReq",
      className: "AppManagerReloadProjectReq"
    },
    final: {
      kind: "struct",
      name: "AppManagerReloadProjectResp",
      className: "AppManagerReloadProjectResp"
    }
  },
  {
    namespace: "appmanager",
    name: "retry_cleanup",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4464,
    finalSchemaId: 4465,
    req: {
      kind: "struct",
      name: "AppManagerRetryCleanupReq",
      className: "AppManagerRetryCleanupReq"
    },
    final: {
      kind: "struct",
      name: "AppManagerRetryCleanupResp",
      className: "AppManagerRetryCleanupResp"
    }
  },
  {
    namespace: "appmanager",
    name: "route_token",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3302,
    finalSchemaId: 3303,
    req: {
      kind: "struct",
      name: "AppRouteTokenReq",
      className: "AppRouteTokenReq"
    },
    final: {
      kind: "struct",
      name: "AppRouteTokenResp",
      className: "AppRouteTokenResp"
    }
  },
  {
    namespace: "appmanager",
    name: "sdk_vendor",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5776,
    finalSchemaId: 5777,
    req: {
      kind: "struct",
      name: "AppManagerSdkVendorReq",
      className: "AppManagerSdkVendorReq"
    },
    final: {
      kind: "struct",
      name: "AppManagerSdkVendorResp",
      className: "AppManagerSdkVendorResp"
    }
  },
  {
    namespace: "appmanager",
    name: "session_create",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3296,
    finalSchemaId: 3297,
    req: {
      kind: "struct",
      name: "AppSessionCreateReq",
      className: "AppSessionCreateReq"
    },
    final: {
      kind: "struct",
      name: "AppSessionCreateResp",
      className: "AppSessionCreateResp"
    }
  },
  {
    namespace: "appmanager",
    name: "session_resolve",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3298,
    finalSchemaId: 3299,
    req: {
      kind: "struct",
      name: "AppSessionResolveReq",
      className: "AppSessionResolveReq"
    },
    final: {
      kind: "struct",
      name: "AppSessionResolveResp",
      className: "AppSessionResolveResp"
    }
  },
  {
    namespace: "appmanager",
    name: "session_revoke",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3300,
    finalSchemaId: 3301,
    req: {
      kind: "struct",
      name: "AppSessionRevokeReq",
      className: "AppSessionRevokeReq"
    },
    final: {
      kind: "struct",
      name: "AppSessionRevokeResp",
      className: "AppSessionRevokeResp"
    }
  },
  {
    namespace: "appmanager",
    name: "unregister",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 2177,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "AppManagerUnregisterReq",
      className: "AppManagerUnregisterReq"
    },
    final: {
      kind: "void",
      name: "void"
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
