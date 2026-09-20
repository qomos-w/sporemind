// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface StoreClientConfig {
  BaseURL: string;
  Channel: string;
  ProductionPubKey: string;
  AuthToken: string;
}

export interface StoreClientConfigView {
  BaseURL: string;
  Channel: string;
  HasAuthToken: boolean;
  HasProductionPubKey: boolean;
}

export interface StoreClientConfigGetReq {

}

export interface StoreClientConfigGetResp {
  Config: StoreClientConfigView;
}

export interface StoreClientConfigSetReq {
  BaseURL?: string | undefined;
  Channel?: string | undefined;
  ProductionPubKey?: string | undefined;
  AuthToken?: string | undefined;
}

export interface StoreClientConfigSetResp {
  Config: StoreClientConfigView;
}

export interface StoreIndexReq {

}

export interface StoreIndexResp {
  GeneratedAt: string;
  FetchedAt: string;
  FromCache: boolean;
  Store: StorePluginView[];
  Community: StoreCommunityView[];
}

export interface StorePluginView {
  Id: string;
  Slug: string;
  DisplayName: string;
  Description: string;
  Publisher: string;
  Channel: string;
  PublisherPubkey: string;
  CurrentVersion: string;
  Versions: StoreVersionView[];
}

export interface StoreVersionView {
  Version: string;
  SdkVersion: string;
  ProtocolVersion: number;
  MinHostVersion: string;
  PayloadSha256: string;
  Size: number;
  Changelog: string;
  CreatedAt: string;
  Compatible: boolean;
  IncompatibleReason?: string | undefined;
}

export interface StoreCommunityView {
  Id: string;
  Slug: string;
  DisplayName: string;
  Description: string;
  RepoUrl: string;
  CommitSha: string;
  Version: string;
  SdkVersion: string;
  ProtocolVersion: number;
  TarballSha256: string;
  MirrorUrl: string;
  Compatible: boolean;
  IncompatibleReason?: string | undefined;
  ManifestId?: string | undefined;
  ManifestName?: string | undefined;
  Permissions?: string[] | undefined;
}

export interface StoreInstallReq {
  Kind: string;
  Slug: string;
  Version?: string | undefined;
}

export interface StoreInstallResp {
  AppId: string;
  Version: string;
  State: string;
  Origin: string;
}
