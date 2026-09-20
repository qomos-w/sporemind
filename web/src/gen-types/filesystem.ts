// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface FileEntry {
  Name: string;
  IsDir: boolean;
  Size: number;
  ModTime: string;
}

export interface FileEntryListResp {
  Items: FileEntry[];
  NumEntries: number;
  Truncated?: boolean | undefined;
}

export interface FileSystemListReq {
  Path: string;
  Depth?: number | undefined;
  Exclude?: string | undefined;
  No_ignore?: boolean | undefined;
  All?: boolean | undefined;
  Detail?: boolean | undefined;
}

export interface FileSystemReadReq {
  Path: string;
  Offset?: number | undefined;
  Limit?: number | undefined;
  Tail?: number | undefined;
  Filter?: string | undefined;
}

export interface FileSystemReadResp {
  Content: string;
  TotalLines: number;
  StartLine: number;
  NumLines: number;
  Truncated: boolean;
  Note?: string | undefined;
}

export interface FileSystemReadBase64Req {
  Path: string;
}

export interface FileSystemReadChunkReq {
  Path: string;
  Offset: number;
  Length: number;
}

export interface FileSystemReadChunkResp {
  Offset: number;
  Length: number;
  Total: number;
  Data: string;
  Note?: string | undefined;
}

export interface FileSystemWriteReq {
  Path: string;
  Content: string;
  Confirm?: boolean | undefined;
}

export interface FileSystemWriteResp {
  Warning?: string | undefined;
}

export interface FileSystemEditReq {
  Path: string;
  Old_string: string;
  New_string: string;
  Replace_all?: boolean | undefined;
  Overwrite?: boolean | undefined;
  Confirm?: boolean | undefined;
}

export interface FileSystemEditHunk {
  Old_start: number;
  Old_lines: number;
  New_start: number;
  New_lines: number;
  Lines: string[];
}

export interface FileSystemEditResp {
  Replacements: number;
  Additions: number;
  Deletions: number;
  Hunks: FileSystemEditHunk[];
}

export interface FileSystemGlobReq {
  Pattern: string;
  Path?: string | undefined;
  Maxdepth?: number | undefined;
  No_ignore?: boolean | undefined;
  Exclude?: string | undefined;
  Order_by?: string | undefined;
}

export interface FileSystemGlobResp {
  Files: string[];
  NumFiles: number;
  Truncated: boolean;
  Note?: string | undefined;
}

export interface FileSystemGrepReq {
  Pattern: string;
  Path?: string | undefined;
  Depth?: number | undefined;
  Ignore_case?: boolean | undefined;
  Output_mode?: string | undefined;
  Glob?: string | undefined;
  Head_limit?: number | undefined;
  Context?: number | undefined;
  Before_context?: number | undefined;
  After_context?: number | undefined;
  Multiline?: boolean | undefined;
  Invert_match?: boolean | undefined;
  Word_regexp?: boolean | undefined;
  No_ignore?: boolean | undefined;
  Recursive?: boolean | undefined;
  Exclude?: string | undefined;
}

export interface FileSystemGrepMatch {
  File: string;
  Line: number;
  Content: string;
}

export interface FileSystemGrepCount {
  File: string;
  Count: number;
}

export interface FileSystemGrepResp {
  Files?: string[] | undefined;
  Counts?: FileSystemGrepCount[] | undefined;
  Matches?: FileSystemGrepMatch[] | undefined;
  NumMatches?: number | undefined;
  Truncated?: boolean | undefined;
  Output_mode?: string | undefined;
  Note?: string | undefined;
}

export interface FileSystemRmReq {
  Path: string;
  Recursive?: boolean | undefined;
  Confirm?: boolean | undefined;
  Force?: boolean | undefined;
}

export interface FileSystemRmResp {
  Removed: string[];
  Count: number;
  Preview: boolean;
}

export interface FileSystemSyncRootsReq {
  Roots: string[];
}

export interface ArchiveExportReq {
  Path: string;
}

export interface ArchiveExportResp {
  Content: string;
  Size: number;
  NumEntries: number;
}

export interface ArchiveImportReq {
  Path: string;
  Content: string;
}

export interface ArchiveImportResp {
  NumEntries: number;
  BytesWritten: number;
}

export interface FileSystemReadBase64Resp {
  Content: string;
  Note?: string | undefined;
}

export interface FileSystemWriteBase64Req {
  Path: string;
  Content: string;
  Confirm?: boolean | undefined;
}

export interface FileSystemRootsResp {
  Home: string;
  Roots: string[];
}
