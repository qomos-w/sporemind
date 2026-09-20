// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface SshExecReq {
  HostId: string;
  Command: string;
  Timeout?: number | undefined;
}

export interface SshExecResp {
  Stdout: string;
  Stderr: string;
  ExitCode: number;
  Truncated: boolean;
  DurationMs: number;
}

export interface SshFolderCreateReq {
  Name: string;
}

export interface SshFolderCreateResp {

}

export interface SshFolderRenameReq {
  From: string;
  To: string;
}

export interface SshFolderRenameResp {

}

export interface SshFolderReorderReq {
  Names: string[];
}

export interface SshFolderReorderResp {

}

export interface SshFolderRemoveReq {
  Name: string;
}

export interface SshFolderRemoveResp {

}
