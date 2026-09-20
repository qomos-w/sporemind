// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "glass_interact",
    name: "bootstrap",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3890,
    finalSchemaId: 3891,
    req: {
      kind: "struct",
      name: "GlassBootstrapReq",
      className: "GlassBootstrapReq"
    },
    final: {
      kind: "struct",
      name: "GlassBootstrapResp",
      className: "GlassBootstrapResp"
    }
  },
  {
    namespace: "glass_interact",
    name: "debug_simulate",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4128,
    finalSchemaId: 4129,
    req: {
      kind: "struct",
      name: "GlassDebugSimulateReq",
      className: "GlassDebugSimulateReq"
    },
    final: {
      kind: "struct",
      name: "GlassDebugSimulateResp",
      className: "GlassDebugSimulateResp"
    }
  },
  {
    namespace: "glass_interact",
    name: "debug_state",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3920,
    finalSchemaId: 3921,
    req: {
      kind: "struct",
      name: "GlassDebugReq",
      className: "GlassDebugReq"
    },
    final: {
      kind: "struct",
      name: "GlassDebugResp",
      className: "GlassDebugResp"
    }
  },
  {
    namespace: "glass_interact",
    name: "interaction_report",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4037,
    finalSchemaId: 4038,
    req: {
      kind: "struct",
      name: "GlassInteractionReportReq",
      className: "GlassInteractionReportReq"
    },
    final: {
      kind: "struct",
      name: "GlassInteractionReportResp",
      className: "GlassInteractionReportResp"
    }
  },
  {
    namespace: "glass_interact",
    name: "session_claim",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3892,
    finalSchemaId: 3893,
    req: {
      kind: "struct",
      name: "GlassSessionClaimReq",
      className: "GlassSessionClaimReq"
    },
    final: {
      kind: "struct",
      name: "GlassSessionClaimResp",
      className: "GlassSessionClaimResp"
    }
  },
  {
    namespace: "glass_interact",
    name: "session_get_state",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3896,
    finalSchemaId: 3897,
    req: {
      kind: "struct",
      name: "GlassGetStateReq",
      className: "GlassGetStateReq"
    },
    final: {
      kind: "struct",
      name: "GlassGetStateResp",
      className: "GlassGetStateResp"
    }
  },
  {
    namespace: "glass_interact",
    name: "speech_chunk",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3899,
    finalSchemaId: 3901,
    req: {
      kind: "struct",
      name: "GlassSpeechChunkReq",
      className: "GlassSpeechChunkReq"
    },
    final: {
      kind: "struct",
      name: "GlassSpeechAck",
      className: "GlassSpeechAck"
    }
  },
  {
    namespace: "glass_interact",
    name: "speech_end",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3900,
    finalSchemaId: 3903,
    req: {
      kind: "struct",
      name: "GlassSpeechEndReq",
      className: "GlassSpeechEndReq"
    },
    final: {
      kind: "struct",
      name: "GlassSpeechEndResp",
      className: "GlassSpeechEndResp"
    }
  },
  {
    namespace: "glass_interact",
    name: "speech_start",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3898,
    finalSchemaId: 3902,
    req: {
      kind: "struct",
      name: "GlassSpeechStartReq",
      className: "GlassSpeechStartReq"
    },
    final: {
      kind: "struct",
      name: "GlassSpeechStartResp",
      className: "GlassSpeechStartResp"
    }
  },
  {
    namespace: "glass_interact",
    name: "telemetry_report",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4080,
    finalSchemaId: 4081,
    req: {
      kind: "struct",
      name: "GlassTelemetryReq",
      className: "GlassTelemetryReq"
    },
    final: {
      kind: "struct",
      name: "GlassTelemetryResp",
      className: "GlassTelemetryResp"
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
