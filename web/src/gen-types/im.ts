// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface ImAccount {
  Id: string;
  Name: string;
  Provider: string;
  Token: string;
  Enabled: boolean;
  AllowUsers?: string[] | undefined;
  ApiBase?: string | undefined;
}

export interface ImAccountView {
  Id: string;
  Name: string;
  Provider: string;
  Enabled: boolean;
  HasToken: boolean;
  AllowUsers?: string[] | undefined;
  ApiBase?: string | undefined;
  BotUsername?: string | undefined;
  Status?: string | undefined;
  StatusDetail?: string | undefined;
}

export interface ImAccountListReq {

}

export interface ImAccountListResp {
  Items: ImAccountView[];
}

export interface ImAccountCreateReq {
  Name: string;
  Provider: string;
  Token: string;
  Enabled?: boolean | undefined;
  AllowUsers?: string[] | undefined;
  ApiBase?: string | undefined;
}

export interface ImAccountCreateResp {
  Account: ImAccountView;
}

export interface ImAccountUpdateReq {
  Id: string;
  Name?: string | undefined;
  Token?: string | undefined;
  Enabled?: boolean | undefined;
  AllowUsers?: string[] | undefined;
  ApiBase?: string | undefined;
}

export interface ImAccountUpdateResp {
  Account: ImAccountView;
}

export interface ImAccountDeleteReq {
  Id: string;
}

export interface ImAccountDeleteResp {

}

export interface ImRoute {
  Id: string;
  AccountId: string;
  AgentActorId: string;
  MountedAt?: string | undefined;
}

export interface ImRouteListReq {

}

export interface ImRouteListResp {
  Items: ImRoute[];
}

export interface ImRouteSetReq {
  AccountId: string;
  AgentActorId: string;
}

export interface ImRouteSetResp {
  Route: ImRoute;
}

export interface ImRouteDeleteReq {
  Id: string;
}

export interface ImRouteDeleteResp {

}

export interface ImStatusReq {

}

export interface ImStatusResp {
  Items: ImAccountView[];
}

export interface ImSendReq {
  AccountId: string;
  ChatId: string;
  Text: string;
}

export interface ImSendResp {
  Sent: boolean;
}
