// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface MediaAccount {
  Id: string;
  Kind: string;
  Name: string;
  Provider: string;
  ApiKey: string;
  Model?: string | undefined;
  BaseUrl?: string | undefined;
  Proxy?: string | undefined;
}

export interface MediaAccountView {
  Id: string;
  Kind: string;
  Name: string;
  Provider: string;
  HasApiKey: boolean;
  Model?: string | undefined;
  BaseUrl?: string | undefined;
  Proxy?: string | undefined;
}

export interface MediaAccountListReq {
  Kind?: string | undefined;
}

export interface MediaAccountListResp {
  Items: MediaAccountView[];
  ActiveId?: string | undefined;
  BoundProvider?: string | undefined;
  BoundModel?: string | undefined;
  BoundAggregator?: string | undefined;
}

export interface MediaAccountCreateReq {
  Kind: string;
  Name: string;
  Provider: string;
  ApiKey?: string | undefined;
  Model?: string | undefined;
  BaseUrl?: string | undefined;
  Proxy?: string | undefined;
}

export interface MediaAccountCreateResp {
  Account: MediaAccountView;
}

export interface MediaAccountUpdateReq {
  Id: string;
  Name?: string | undefined;
  Provider?: string | undefined;
  ApiKey?: string | undefined;
  Model?: string | undefined;
  BaseUrl?: string | undefined;
  Proxy?: string | undefined;
}

export interface MediaAccountUpdateResp {
  Account: MediaAccountView;
}

export interface MediaAccountDeleteReq {
  Id: string;
}

export interface MediaAccountDeleteResp {

}

export interface MediaAccountActivateReq {
  Kind: string;
  Id: string;
}

export interface MediaAccountActivateResp {
  ActiveId: string;
}

export interface MediaActiveAccountReq {
  Kind: string;
}

export interface MediaActiveAccountResp {
  Account: MediaAccount;
  BoundProvider?: string | undefined;
  BoundModel?: string | undefined;
  BoundAggregator?: string | undefined;
}
