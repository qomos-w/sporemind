// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface AppManagerIconNamesReq {
  Query?: string | undefined;
  Category?: string | undefined;
}

export interface AppManagerIconEntry {
  Name: string;
  Label: string;
  Keywords: string[];
  Category: string;
}

export interface AppManagerIconNamesResp {
  Items: AppManagerIconEntry[];
}
