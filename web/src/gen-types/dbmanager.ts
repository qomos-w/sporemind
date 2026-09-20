// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface DbProfile {
  Id: string;
  Name: string;
  Backend: string;
  Endpoint?: string | undefined;
  Database?: string | undefined;
  TunnelRef?: string | undefined;
  Username?: string | undefined;
  AccessKey?: string | undefined;
}

export interface DbProfileView {
  Id: string;
  Name: string;
  Backend: string;
  Endpoint?: string | undefined;
  Database?: string | undefined;
  TunnelRef?: string | undefined;
  Username?: string | undefined;
  AccessKey?: string | undefined;
  HasPassword: boolean;
  HasSecret: boolean;
  HasToken: boolean;
}

export interface DbProfileSaveReq {
  Id: string;
  Name: string;
  Backend: string;
  Endpoint?: string | undefined;
  Database?: string | undefined;
  TunnelRef?: string | undefined;
  Username?: string | undefined;
  AccessKey?: string | undefined;
  Password?: string | undefined;
  Secret?: string | undefined;
  Token?: string | undefined;
}

export interface DbProfileSaveResp {
  Profile: DbProfileView;
}

export interface DbProfileListReq {

}

export interface DbProfileListResp {
  Items: DbProfileView[];
}

export interface DbProfileGetReq {
  Id: string;
}

export interface DbProfileGetResp {
  Profile: DbProfileView;
}

export interface DbProfileRemoveReq {
  Id: string;
}

export interface DbProfileRemoveResp {

}

export interface DbProfileResolveReq {
  Id: string;
}

export interface DbProfileResolveResp {
  Username: string;
  Password: string;
  AccessKey: string;
  Secret: string;
  Token: string;
}

export interface DbProfileLookupReq {
  Id: string;
}

export interface DbProfileLookupResp {
  Profile: DbProfile;
}
