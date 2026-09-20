// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface ShellExecReq {
  Command: string;
  Args?: string[] | undefined;
  Dir?: string | undefined;
  Timeout?: number | undefined;
  Confirm?: boolean | undefined;
}

export interface ShellExecResp {
  Stdout: string;
  Stderr: string;
  ExitCode: number;
}

export interface ShellBashReq {
  Command: string;
  Args?: string[] | undefined;
  Dir?: string | undefined;
  Timeout?: number | undefined;
}

export interface ShellBashResp {
  Stdout: string;
  Stderr: string;
  ExitCode: number;
  Truncated?: boolean | undefined;
  Interrupted?: boolean | undefined;
  ReturnCodeInterpretation?: string | undefined;
}

export interface ShellChunk {
  Kind: string;
  Text?: string | undefined;
  ExitCode?: number | undefined;
  Stdout?: string | undefined;
  Stderr?: string | undefined;
  Truncated?: boolean | undefined;
  Interrupted?: boolean | undefined;
  ReturnCodeInterpretation?: string | undefined;
  DirWarning?: string | undefined;
}
