// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface WorkspaceLogStreamEntry {
  Timestamp: string;
  Level: string;
  CallerFile: string;
  CallerLine: number;
  Message: string;
  Fields?: Record<string, unknown> | undefined;
  Source: string;
  Seq: number;
}

export interface WorkspaceLogStreamEvent {
  Entries: WorkspaceLogStreamEntry[];
}
