// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "voice",
    name: "activate_account",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1818,
    finalSchemaId: 1819,
    req: {
      kind: "struct",
      name: "VoiceAccountActivateReq",
      className: "VoiceAccountActivateReq"
    },
    final: {
      kind: "struct",
      name: "VoiceAccountActivateResp",
      className: "VoiceAccountActivateResp"
    }
  },
  {
    namespace: "voice",
    name: "clone",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1831,
    finalSchemaId: 1832,
    req: {
      kind: "struct",
      name: "VoiceCloneReq",
      className: "VoiceCloneReq"
    },
    final: {
      kind: "struct",
      name: "VoiceCloneResp",
      className: "VoiceCloneResp"
    }
  },
  {
    namespace: "voice",
    name: "config_export",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 1826,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "VoiceConfigExportResp",
      className: "VoiceConfigExportResp"
    }
  },
  {
    namespace: "voice",
    name: "config_import",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1827,
    finalSchemaId: 1828,
    req: {
      kind: "struct",
      name: "VoiceConfigImportReq",
      className: "VoiceConfigImportReq"
    },
    final: {
      kind: "struct",
      name: "VoiceConfigImportResp",
      className: "VoiceConfigImportResp"
    }
  },
  {
    namespace: "voice",
    name: "create_account",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1812,
    finalSchemaId: 1813,
    req: {
      kind: "struct",
      name: "VoiceAccountCreateReq",
      className: "VoiceAccountCreateReq"
    },
    final: {
      kind: "struct",
      name: "VoiceAccountCreateResp",
      className: "VoiceAccountCreateResp"
    }
  },
  {
    namespace: "voice",
    name: "delete_account",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1816,
    finalSchemaId: 1817,
    req: {
      kind: "struct",
      name: "VoiceAccountDeleteReq",
      className: "VoiceAccountDeleteReq"
    },
    final: {
      kind: "struct",
      name: "VoiceAccountDeleteResp",
      className: "VoiceAccountDeleteResp"
    }
  },
  {
    namespace: "voice",
    name: "delete_hotwords",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3829,
    finalSchemaId: 3830,
    req: {
      kind: "struct",
      name: "VoiceHotwordsDeleteReq",
      className: "VoiceHotwordsDeleteReq"
    },
    final: {
      kind: "struct",
      name: "VoiceHotwordsDeleteResp",
      className: "VoiceHotwordsDeleteResp"
    }
  },
  {
    namespace: "voice",
    name: "design",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1833,
    finalSchemaId: 1834,
    req: {
      kind: "struct",
      name: "VoiceDesignReq",
      className: "VoiceDesignReq"
    },
    final: {
      kind: "struct",
      name: "VoiceDesignResp",
      className: "VoiceDesignResp"
    }
  },
  {
    namespace: "voice",
    name: "get_hotwords",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3827,
    finalSchemaId: 3828,
    req: {
      kind: "struct",
      name: "VoiceHotwordsGetReq",
      className: "VoiceHotwordsGetReq"
    },
    final: {
      kind: "struct",
      name: "VoiceHotwordsGetResp",
      className: "VoiceHotwordsGetResp"
    }
  },
  {
    namespace: "voice",
    name: "get_notify_config",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 1830,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "VoiceNotifyConfigResp",
      className: "VoiceNotifyConfigResp"
    }
  },
  {
    namespace: "voice",
    name: "list_accounts",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1810,
    finalSchemaId: 1811,
    req: {
      kind: "struct",
      name: "VoiceAccountListReq",
      className: "VoiceAccountListReq"
    },
    final: {
      kind: "struct",
      name: "VoiceAccountListResp",
      className: "VoiceAccountListResp"
    }
  },
  {
    namespace: "voice",
    name: "recognize",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1821,
    finalSchemaId: 1822,
    req: {
      kind: "struct",
      name: "VoiceRecognizeReq",
      className: "VoiceRecognizeReq"
    },
    final: {
      kind: "struct",
      name: "VoiceRecognizeResp",
      className: "VoiceRecognizeResp"
    }
  },
  {
    namespace: "voice",
    name: "set_hotwords",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3825,
    finalSchemaId: 3826,
    req: {
      kind: "struct",
      name: "VoiceHotwordsSetReq",
      className: "VoiceHotwordsSetReq"
    },
    final: {
      kind: "struct",
      name: "VoiceHotwordsSetResp",
      className: "VoiceHotwordsSetResp"
    }
  },
  {
    namespace: "voice",
    name: "set_notify_config",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1829,
    finalSchemaId: 1830,
    req: {
      kind: "struct",
      name: "VoiceNotifyConfig",
      className: "VoiceNotifyConfig"
    },
    final: {
      kind: "struct",
      name: "VoiceNotifyConfigResp",
      className: "VoiceNotifyConfigResp"
    }
  },
  {
    namespace: "voice",
    name: "synthesize",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1823,
    finalSchemaId: 1824,
    req: {
      kind: "struct",
      name: "VoiceSynthesizeReq",
      className: "VoiceSynthesizeReq"
    },
    final: {
      kind: "struct",
      name: "VoiceSynthesizeResp",
      className: "VoiceSynthesizeResp"
    }
  },
  {
    namespace: "voice",
    name: "update_account",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1814,
    finalSchemaId: 1815,
    req: {
      kind: "struct",
      name: "VoiceAccountUpdateReq",
      className: "VoiceAccountUpdateReq"
    },
    final: {
      kind: "struct",
      name: "VoiceAccountUpdateResp",
      className: "VoiceAccountUpdateResp"
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
