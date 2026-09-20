// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface ShellSessionOpenReq {
  Cols?: number | undefined;
  Rows?: number | undefined;
  WorkingDirectory?: string | undefined;
}

export interface ShellSessionOpenResp {
  SessionId: string;
  Mode: string;
}

export interface ShellSessionWriteReq {
  SessionId: string;
  Data: string;
}

export interface ShellSessionWriteResp {

}

export interface ShellSessionResizeReq {
  SessionId: string;
  Cols: number;
  Rows: number;
}

export interface ShellSessionResizeResp {

}

export interface ShellSessionCloseReq {
  SessionId: string;
}

export interface ShellSessionCloseResp {

}

export interface ShellSessionOutputEvent {
  SessionId: string;
  Idx: number;
  Kind: string;
  Data: string;
  ExitCode?: number | undefined;
  Mode?: string | undefined;
}
