// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "computeruse",
    name: "capabilities",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 537,
    finalSchemaId: 538,
    req: {
      kind: "struct",
      name: "ComputerUseCapabilitiesReq",
      className: "ComputerUseCapabilitiesReq"
    },
    final: {
      kind: "struct",
      name: "ComputerUseCapabilitiesResp",
      className: "ComputerUseCapabilitiesResp"
    }
  },
  {
    namespace: "computeruse",
    name: "cursor_position",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 527,
    finalSchemaId: 526,
    req: {
      kind: "struct",
      name: "ComputerUseCursorPositionReq",
      className: "ComputerUseCursorPositionReq"
    },
    final: {
      kind: "struct",
      name: "ComputerUseCursorPosResp",
      className: "ComputerUseCursorPosResp"
    }
  },
  {
    namespace: "computeruse",
    name: "get_window_info",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 521,
    finalSchemaId: 522,
    req: {
      kind: "struct",
      name: "ComputerUseGetWindowInfoReq",
      className: "ComputerUseGetWindowInfoReq"
    },
    final: {
      kind: "struct",
      name: "ComputerUseWindowInfoDetailed",
      className: "ComputerUseWindowInfoDetailed"
    }
  },
  {
    namespace: "computeruse",
    name: "list_displays",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 525,
    finalSchemaId: 524,
    req: {
      kind: "struct",
      name: "ComputerUseListDisplaysReq",
      className: "ComputerUseListDisplaysReq"
    },
    final: {
      kind: "struct",
      name: "ComputerUseDisplaysResp",
      className: "ComputerUseDisplaysResp"
    }
  },
  {
    namespace: "computeruse",
    name: "list_elements",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 529,
    finalSchemaId: 530,
    req: {
      kind: "struct",
      name: "ComputerUseListElementsReq",
      className: "ComputerUseListElementsReq"
    },
    final: {
      kind: "struct",
      name: "ComputerUseListElementsResp",
      className: "ComputerUseListElementsResp"
    }
  },
  {
    namespace: "computeruse",
    name: "list_processes",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 516,
    finalSchemaId: 517,
    req: {
      kind: "struct",
      name: "ComputerUseListProcessesReq",
      className: "ComputerUseListProcessesReq"
    },
    final: {
      kind: "struct",
      name: "ComputerUseProcessesResp",
      className: "ComputerUseProcessesResp"
    }
  },
  {
    namespace: "computeruse",
    name: "list_windows",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 518,
    finalSchemaId: 520,
    req: {
      kind: "struct",
      name: "ComputerUseListWindowsReq",
      className: "ComputerUseListWindowsReq"
    },
    final: {
      kind: "struct",
      name: "ComputerUseWindowsResp",
      className: "ComputerUseWindowsResp"
    }
  },
  {
    namespace: "computeruse",
    name: "ocr",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 531,
    finalSchemaId: 533,
    req: {
      kind: "struct",
      name: "ComputerUseOcrReq",
      className: "ComputerUseOcrReq"
    },
    final: {
      kind: "struct",
      name: "ComputerUseOcrResp",
      className: "ComputerUseOcrResp"
    }
  },
  {
    namespace: "computeruse",
    name: "screenshot",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 500,
    finalSchemaId: 501,
    req: {
      kind: "struct",
      name: "ComputerUseScreenshotReq",
      className: "ComputerUseScreenshotReq"
    },
    final: {
      kind: "struct",
      name: "ComputerUseScreenshotResp",
      className: "ComputerUseScreenshotResp"
    }
  },
  {
    namespace: "computeruse",
    name: "setup_ocr",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 534,
    finalSchemaId: 535,
    req: {
      kind: "struct",
      name: "ComputerUseSetupOcrReq",
      className: "ComputerUseSetupOcrReq"
    },
    final: {
      kind: "struct",
      name: "ComputerUseSetupOcrResp",
      className: "ComputerUseSetupOcrResp"
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
