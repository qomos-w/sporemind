// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function capabilities(client: GosporeClient, req: systemTypes.ComputerUseCapabilitiesReq, opts?: InvokeOptions): Promise<systemTypes.ComputerUseCapabilitiesResp> {
  return client.invoke<systemTypes.ComputerUseCapabilitiesReq, systemTypes.ComputerUseCapabilitiesResp>("computeruse.capabilities", req, { reqSchemaId: 537, resSchemaId: 538, ...opts });
}

export const capabilities_meta = {
  callable: "computeruse.capabilities",
  name: "capabilities",
  reqSchemaId: 537,
  resSchemaId: 538,
} as const;

export async function cursorPosition(client: GosporeClient, req: systemTypes.ComputerUseCursorPositionReq, opts?: InvokeOptions): Promise<systemTypes.ComputerUseCursorPosResp> {
  return client.invoke<systemTypes.ComputerUseCursorPositionReq, systemTypes.ComputerUseCursorPosResp>("computeruse.cursor_position", req, { reqSchemaId: 527, resSchemaId: 526, ...opts });
}

export const cursorPosition_meta = {
  callable: "computeruse.cursor_position",
  name: "cursor_position",
  reqSchemaId: 527,
  resSchemaId: 526,
} as const;

export async function getWindowInfo(client: GosporeClient, req: systemTypes.ComputerUseGetWindowInfoReq, opts?: InvokeOptions): Promise<systemTypes.ComputerUseWindowInfoDetailed> {
  return client.invoke<systemTypes.ComputerUseGetWindowInfoReq, systemTypes.ComputerUseWindowInfoDetailed>("computeruse.get_window_info", req, { reqSchemaId: 521, resSchemaId: 522, ...opts });
}

export const getWindowInfo_meta = {
  callable: "computeruse.get_window_info",
  name: "get_window_info",
  reqSchemaId: 521,
  resSchemaId: 522,
} as const;

export async function listDisplays(client: GosporeClient, req: systemTypes.ComputerUseListDisplaysReq, opts?: InvokeOptions): Promise<systemTypes.ComputerUseDisplaysResp> {
  return client.invoke<systemTypes.ComputerUseListDisplaysReq, systemTypes.ComputerUseDisplaysResp>("computeruse.list_displays", req, { reqSchemaId: 525, resSchemaId: 524, ...opts });
}

export const listDisplays_meta = {
  callable: "computeruse.list_displays",
  name: "list_displays",
  reqSchemaId: 525,
  resSchemaId: 524,
} as const;

export async function listElements(client: GosporeClient, req: systemTypes.ComputerUseListElementsReq, opts?: InvokeOptions): Promise<systemTypes.ComputerUseListElementsResp> {
  return client.invoke<systemTypes.ComputerUseListElementsReq, systemTypes.ComputerUseListElementsResp>("computeruse.list_elements", req, { reqSchemaId: 529, resSchemaId: 530, ...opts });
}

export const listElements_meta = {
  callable: "computeruse.list_elements",
  name: "list_elements",
  reqSchemaId: 529,
  resSchemaId: 530,
} as const;

export async function listProcesses(client: GosporeClient, req: systemTypes.ComputerUseListProcessesReq, opts?: InvokeOptions): Promise<systemTypes.ComputerUseProcessesResp> {
  return client.invoke<systemTypes.ComputerUseListProcessesReq, systemTypes.ComputerUseProcessesResp>("computeruse.list_processes", req, { reqSchemaId: 516, resSchemaId: 517, ...opts });
}

export const listProcesses_meta = {
  callable: "computeruse.list_processes",
  name: "list_processes",
  reqSchemaId: 516,
  resSchemaId: 517,
} as const;

export async function listWindows(client: GosporeClient, req: systemTypes.ComputerUseListWindowsReq, opts?: InvokeOptions): Promise<systemTypes.ComputerUseWindowsResp> {
  return client.invoke<systemTypes.ComputerUseListWindowsReq, systemTypes.ComputerUseWindowsResp>("computeruse.list_windows", req, { reqSchemaId: 518, resSchemaId: 520, ...opts });
}

export const listWindows_meta = {
  callable: "computeruse.list_windows",
  name: "list_windows",
  reqSchemaId: 518,
  resSchemaId: 520,
} as const;

export async function ocr(client: GosporeClient, req: systemTypes.ComputerUseOcrReq, opts?: InvokeOptions): Promise<systemTypes.ComputerUseOcrResp> {
  return client.invoke<systemTypes.ComputerUseOcrReq, systemTypes.ComputerUseOcrResp>("computeruse.ocr", req, { reqSchemaId: 531, resSchemaId: 533, ...opts });
}

export const ocr_meta = {
  callable: "computeruse.ocr",
  name: "ocr",
  reqSchemaId: 531,
  resSchemaId: 533,
} as const;

export async function screenshot(client: GosporeClient, req: systemTypes.ComputerUseScreenshotReq, opts?: InvokeOptions): Promise<systemTypes.ComputerUseScreenshotResp> {
  return client.invoke<systemTypes.ComputerUseScreenshotReq, systemTypes.ComputerUseScreenshotResp>("computeruse.screenshot", req, { reqSchemaId: 500, resSchemaId: 501, ...opts });
}

export const screenshot_meta = {
  callable: "computeruse.screenshot",
  name: "screenshot",
  reqSchemaId: 500,
  resSchemaId: 501,
} as const;

export async function setupOcr(client: GosporeClient, req: systemTypes.ComputerUseSetupOcrReq, opts?: InvokeOptions): Promise<systemTypes.ComputerUseSetupOcrResp> {
  return client.invoke<systemTypes.ComputerUseSetupOcrReq, systemTypes.ComputerUseSetupOcrResp>("computeruse.setup_ocr", req, { reqSchemaId: 534, resSchemaId: 535, ...opts });
}

export const setupOcr_meta = {
  callable: "computeruse.setup_ocr",
  name: "setup_ocr",
  reqSchemaId: 534,
  resSchemaId: 535,
} as const;

