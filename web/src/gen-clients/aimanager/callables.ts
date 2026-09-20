// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "aimanager",
    name: "aggregator_configure",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1425,
    finalSchemaId: 1426,
    req: {
      kind: "struct",
      name: "AIManagerAggregatorConfigureReq",
      className: "AIManagerAggregatorConfigureReq"
    },
    final: {
      kind: "struct",
      name: "AIManagerAggregatorConfigureResp",
      className: "AIManagerAggregatorConfigureResp"
    }
  },
  {
    namespace: "aimanager",
    name: "aggregator_get",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1427,
    finalSchemaId: 1428,
    req: {
      kind: "struct",
      name: "AIManagerAggregatorGetReq",
      className: "AIManagerAggregatorGetReq"
    },
    final: {
      kind: "struct",
      name: "AIManagerAggregatorGetResp",
      className: "AIManagerAggregatorGetResp"
    }
  },
  {
    namespace: "aimanager",
    name: "aggregator_list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 351,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "AggregatorDescriptorListResp",
      className: "AggregatorDescriptorListResp"
    }
  },
  {
    namespace: "aimanager",
    name: "aggregator_set_disabled",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1454,
    finalSchemaId: 1455,
    req: {
      kind: "struct",
      name: "AIManagerAggregatorSetDisabledReq",
      className: "AIManagerAggregatorSetDisabledReq"
    },
    final: {
      kind: "struct",
      name: "AIManagerAggregatorSetDisabledResp",
      className: "AIManagerAggregatorSetDisabledResp"
    }
  },
  {
    namespace: "aimanager",
    name: "config_export",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 1432,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "AIManagerConfigExportResp",
      className: "AIManagerConfigExportResp"
    }
  },
  {
    namespace: "aimanager",
    name: "config_import",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1433,
    finalSchemaId: 1434,
    req: {
      kind: "struct",
      name: "AIManagerConfigImportReq",
      className: "AIManagerConfigImportReq"
    },
    final: {
      kind: "struct",
      name: "AIManagerConfigImportResp",
      className: "AIManagerConfigImportResp"
    }
  },
  {
    namespace: "aimanager",
    name: "fetch_openrouter_models",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 4950,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "AIManagerFetchOpenRouterModelsResp",
      className: "AIManagerFetchOpenRouterModelsResp"
    }
  },
  {
    namespace: "aimanager",
    name: "list_units",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1456,
    finalSchemaId: 1457,
    req: {
      kind: "struct",
      name: "AIManagerListUnitsReq",
      className: "AIManagerListUnitsReq"
    },
    final: {
      kind: "struct",
      name: "AIManagerListUnitsResp",
      className: "AIManagerListUnitsResp"
    }
  },
  {
    namespace: "aimanager",
    name: "model_defaults_get",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 4946,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "AIManagerModelDefaultsGetResp",
      className: "AIManagerModelDefaultsGetResp"
    }
  },
  {
    namespace: "aimanager",
    name: "model_defaults_set",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 4947,
    finalSchemaId: 4948,
    req: {
      kind: "struct",
      name: "AIManagerModelDefaultsSetReq",
      className: "AIManagerModelDefaultsSetReq"
    },
    final: {
      kind: "struct",
      name: "AIManagerModelDefaultsSetResp",
      className: "AIManagerModelDefaultsSetResp"
    }
  },
  {
    namespace: "aimanager",
    name: "model_list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 1417,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "ModelListResp",
      className: "ModelListResp"
    }
  },
  {
    namespace: "aimanager",
    name: "provider_configure",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1413,
    finalSchemaId: 1414,
    req: {
      kind: "struct",
      name: "AIManagerProviderConfigureReq",
      className: "AIManagerProviderConfigureReq"
    },
    final: {
      kind: "struct",
      name: "AIManagerProviderConfigureResp",
      className: "AIManagerProviderConfigureResp"
    }
  },
  {
    namespace: "aimanager",
    name: "provider_fetch_models",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1418,
    finalSchemaId: 1419,
    req: {
      kind: "struct",
      name: "AIManagerProviderFetchModelsReq",
      className: "AIManagerProviderFetchModelsReq"
    },
    final: {
      kind: "struct",
      name: "AIManagerProviderFetchModelsResp",
      className: "AIManagerProviderFetchModelsResp"
    }
  },
  {
    namespace: "aimanager",
    name: "provider_list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 1416,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "ProviderListResp",
      className: "ProviderListResp"
    }
  },
  {
    namespace: "aimanager",
    name: "provider_record_probe",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1447,
    finalSchemaId: 1448,
    req: {
      kind: "struct",
      name: "AIManagerProviderRecordProbeReq",
      className: "AIManagerProviderRecordProbeReq"
    },
    final: {
      kind: "struct",
      name: "AIManagerProviderRecordProbeResp",
      className: "AIManagerProviderRecordProbeResp"
    }
  },
  {
    namespace: "aimanager",
    name: "provider_reset_health",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1445,
    finalSchemaId: 1446,
    req: {
      kind: "struct",
      name: "AIManagerProviderResetHealthReq",
      className: "AIManagerProviderResetHealthReq"
    },
    final: {
      kind: "struct",
      name: "AIManagerProviderResetHealthResp",
      className: "AIManagerProviderResetHealthResp"
    }
  },
  {
    namespace: "aimanager",
    name: "provider_set_disabled",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1452,
    finalSchemaId: 1453,
    req: {
      kind: "struct",
      name: "AIManagerProviderSetDisabledReq",
      className: "AIManagerProviderSetDisabledReq"
    },
    final: {
      kind: "struct",
      name: "AIManagerProviderSetDisabledResp",
      className: "AIManagerProviderSetDisabledResp"
    }
  },
  {
    namespace: "aimanager",
    name: "provider_set_token_plan",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1441,
    finalSchemaId: 1442,
    req: {
      kind: "struct",
      name: "AIManagerProviderSetTokenPlanReq",
      className: "AIManagerProviderSetTokenPlanReq"
    },
    final: {
      kind: "struct",
      name: "AIManagerProviderSetTokenPlanResp",
      className: "AIManagerProviderSetTokenPlanResp"
    }
  },
  {
    namespace: "aimanager",
    name: "unit_health_list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 1451,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "AIManagerUnitHealthListResp",
      className: "AIManagerUnitHealthListResp"
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
