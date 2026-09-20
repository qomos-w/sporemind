import type { DiagnosticSeverity } from "../../domain/types";
import { client } from "../../application/generated-client";
import * as workspace from "../../gen-clients/workspace/client";
import type { FrontendErrorReport } from "../../gen-types/desktop";

let nextId = 0;

function generateRequestId(): string {
  return `fe-err-${Date.now()}-${nextId++}`;
}

function buildReport(
  severity: DiagnosticSeverity,
  message: string,
  component: string,
  sessionId: string,
  stackTrace?: string
): FrontendErrorReport {
  return {
    RequestId: generateRequestId(),
    Severity: severity,
    SourceComponent: component || "unknown",
    Message: message,
    StackTrace: stackTrace || "",
    UserAgent: typeof navigator !== "undefined" ? navigator.userAgent : "",
    SessionId: sessionId,
  };
}

function safeReport(report: FrontendErrorReport): void {
  workspace.reportError(client, report).catch(() => {});
}

export function enableErrorCapture(sessionID: string): () => void {
  const handleErrorEvent = (event: ErrorEvent) => {
    const report = buildReport(
      "error",
      event.message || "Uncaught error",
      event.filename || "unknown",
      sessionID,
      event.error?.stack || ""
    );
    safeReport(report);
  };

  const handleRejectionEvent = (event: PromiseRejectionEvent) => {
    const reason = event.reason;
    const message =
      typeof reason === "string"
        ? reason
        : reason?.message || "Unhandled promise rejection";
    const stack = typeof reason === "object" && reason?.stack ? reason.stack : "";
    const report = buildReport("error", message, "promise", sessionID, stack);
    safeReport(report);
  };

  window.addEventListener("error", handleErrorEvent);
  window.addEventListener("unhandledrejection", handleRejectionEvent);

  return () => {
    window.removeEventListener("error", handleErrorEvent);
    window.removeEventListener("unhandledrejection", handleRejectionEvent);
  };
}

export function reportError(
  severity: DiagnosticSeverity,
  message: string,
  component: string,
  sessionID: string
): void {
  const report = buildReport(severity, message, component, sessionID);
  safeReport(report);
}
