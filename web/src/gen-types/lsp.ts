// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface LspPositionParams {
  Uri: string;
  Line: number;
  Character: number;
  RootUri: string;
  Language: string;
}

export interface LspRangeParams {
  Uri: string;
  StartLine: number;
  StartChar: number;
  EndLine: number;
  EndChar: number;
  RootUri: string;
  Language: string;
}

export interface LspDidOpenReq {
  Uri: string;
  LanguageId: string;
  Text: string;
  Version: number;
  RootUri: string;
  Language: string;
}

export interface LspDidChangeReq {
  Uri: string;
  Version: number;
  Changes: string;
  RootUri: string;
  Language: string;
}

export interface LspDidCloseReq {
  Uri: string;
  RootUri: string;
  Language: string;
}

export interface LspUriReq {
  Uri: string;
  RootUri: string;
  Language: string;
}

export interface LspInitializeReq {
  RootUri: string;
  Language: string;
}

export interface LspRenameReq {
  Uri: string;
  Line: number;
  Character: number;
  NewName: string;
  RootUri: string;
  Language: string;
}

export interface LspReferencesReq {
  Uri: string;
  Line: number;
  Character: number;
  IncludeDeclaration: boolean;
  RootUri: string;
  Language: string;
}

export interface LspShutdownReq {
  RootUri: string;
  Language: string;
}

export interface LspClearCacheReq {
  Evicted: number;
}

export interface LspJsonResp {
  Json: string;
}

export interface LspDiagnosticsEvent {
  Uri: string;
  Version: number;
  Diagnostics: string;
}

export interface LspLanguageState {
  Language: string;
  Enabled: boolean;
}

export interface LspStateResp {
  Languages: LspLanguageState[];
}

export interface LspStateSaveReq {
  Language: string;
  Enabled: boolean;
}

export interface LspStatusReq {
  Language: string;
}

export interface LspLanguageInstallState {
  Language: string;
  Installed: boolean;
  Version?: string | undefined;
  BinaryPath?: string | undefined;
  DownloadSource?: string | undefined;
}

export interface LspStatusResp {
  Languages: LspLanguageInstallState[];
}

export interface LspInstallReq {
  Language: string;
}

export interface LspInstallResp {
  Started: boolean;
}

export interface LspInstallProgressEvent {
  Language: string;
  State: string;
  Percent: number;
  Error?: string | undefined;
}
