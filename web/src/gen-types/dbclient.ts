// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface DbTreeNode {
  Path: string;
  Kind: string;
  Label: string;
  Meta?: Record<string, string> | undefined;
}

export interface DbRows {
  Columns: string[];
  Rows: string[][];
  Truncated: boolean;
  Message?: string | undefined;
}

export interface DbDialTestReq {
  ProfileId: string;
}

export interface DbDialTestResp {
  Ok: boolean;
  LatencyMs: number;
  ServerVersion?: string | undefined;
  Error?: string | undefined;
}

export interface DbTreeReq {
  ProfileId: string;
  Path: string;
  Cursor?: string | undefined;
}

export interface DbTreeResp {
  Nodes: DbTreeNode[];
  Cursor?: string | undefined;
  HasMore: boolean;
}

export interface DbReadReq {
  ProfileId: string;
  Path: string;
  Limit?: number | undefined;
  Offset?: number | undefined;
}

export interface DbQueryReq {
  ProfileId: string;
  Text: string;
  Mode?: string | undefined;
}

export interface DbCloseReq {
  ProfileId: string;
}

export interface DbCloseResp {

}

export interface DbColumnDef {
  Name: string;
  DataType: string;
  Nullable: boolean;
  Default?: string | undefined;
  Key?: string | undefined;
  Extra?: string | undefined;
}

export interface DbIndexDef {
  Name: string;
  Columns: string;
  Unique: boolean;
}

export interface DbDescribeReq {
  ProfileId: string;
  Path: string;
}

export interface DbDescribeResp {
  Columns: DbColumnDef[];
  Indexes: DbIndexDef[];
}

export interface DbObjectEntry {
  Name: string;
  Path: string;
  IsDir: boolean;
  Size: number;
  Modified?: string | undefined;
  ContentType?: string | undefined;
}

export interface DbObjectListReq {
  ProfileId: string;
  Path: string;
  Cursor?: string | undefined;
  Limit?: number | undefined;
}

export interface DbObjectListResp {
  Entries: DbObjectEntry[];
  Cursor?: string | undefined;
  HasMore: boolean;
}

export interface DbObjectReadReq {
  ProfileId: string;
  Path: string;
}

export interface DbObjectReadResp {
  Content: string;
  Size: number;
  Truncated: boolean;
  ContentType?: string | undefined;
  Modified?: string | undefined;
}

export interface DbObjectWriteReq {
  ProfileId: string;
  Path: string;
  Content: string;
  ContentType?: string | undefined;
}

export interface DbObjectWriteResp {
  Size: number;
}

export interface DbObjectDeleteReq {
  ProfileId: string;
  Path: string;
}

export interface DbObjectDeleteResp {

}

export interface DbObjectMkdirReq {
  ProfileId: string;
  ParentPath: string;
  Name: string;
}

export interface DbObjectMkdirResp {

}

export interface DbObjectStatReq {
  ProfileId: string;
  Path: string;
}

export interface DbObjectStatResp {
  Size: number;
  IsDir: boolean;
  Modified?: string | undefined;
  ContentType?: string | undefined;
}
