// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { ShellSessionOutputEvent } from './shell.session';

export interface ShellSessionFetchReq {
  SessionId: string;
  FromIdx: number;
}

export interface ShellSessionFetchResp {
  Chunks: ShellSessionOutputEvent[];
  NextIdx: number;
  Truncated?: boolean | undefined;
}
