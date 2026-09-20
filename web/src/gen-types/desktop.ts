// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface DesktopWindowState {
  X: number;
  Y: number;
  Width: number;
  Height: number;
  Maximised: boolean;
}

export interface FrontendErrorReport {
  RequestId: string;
  Severity: string;
  SourceComponent: string;
  Message: string;
  StackTrace: string;
  UserAgent: string;
  SessionId: string;
}

export interface RootDirEntry {
  Name: string;
  Path: string;
}
