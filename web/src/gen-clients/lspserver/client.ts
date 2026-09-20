// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export type LspDiagnosticsHandler = (payload: systemTypes.LspDiagnosticsEvent) => void;

export function OnLspDiagnostics(client: GosporeClient, handler: LspDiagnosticsHandler): () => void {
  return client.events.onService("lsp", "lsp.diagnostics", (payload) => handler(payload as systemTypes.LspDiagnosticsEvent));
}

export function OffLspDiagnostics(cancel: () => void): void {
  cancel();
}

export type LspInstallProgressHandler = (payload: systemTypes.LspInstallProgressEvent) => void;

export function OnLspInstallProgress(client: GosporeClient, handler: LspInstallProgressHandler): () => void {
  return client.events.onService("lsp", "lsp.install_progress", (payload) => handler(payload as systemTypes.LspInstallProgressEvent));
}

export function OffLspInstallProgress(cancel: () => void): void {
  cancel();
}

