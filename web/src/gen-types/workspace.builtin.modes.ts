// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface BuiltinMode {
  CardId: string;
  Name: string;
  Title: string;
  Icon: string;
}

export interface WorkspaceBuiltinModesListReq {

}

export interface WorkspaceBuiltinModesListResp {
  Modes: BuiltinMode[];
}
