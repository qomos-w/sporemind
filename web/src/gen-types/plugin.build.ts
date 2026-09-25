// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { PluginAbi } from './plugin';
import { AppManifest, AppStatus } from './app';

export interface NativeBuildContract {
  Runtime: string;
  TargetOS: string;
  TargetArch: string;
  ArtifactPath: string;
  ManifestPath: string;
  EntrySymbol?: string | undefined;
  ArtifactHash?: string | undefined;
}

export interface NativeBuildResult {
  Success: boolean;
  ArtifactPath: string;
  ArtifactHash: string;
  Diagnostic: string;
}

export interface NativeBuildReq {
  ProjectId: string;
  AppId: string;
  EntryModule?: string | undefined;
  AppDir?: string | undefined;
  SourceRoot?: string | undefined;
  TargetOS?: string | undefined;
  TargetArch?: string | undefined;
  ArtifactHash?: string | undefined;
  AgentId?: string | undefined;
  RequestId?: string | undefined;
  Mode?: string | undefined;
}

export interface NativeBuildResp {
  Result: NativeBuildResult;
  ManifestPath: string;
  Abi: PluginAbi;
}

export interface PluginArtifactLoadReq {
  Manifest: AppManifest;
  Abi: PluginAbi;
  ArtifactPath: string;
  ArtifactHash?: string | undefined;
  EntrySymbol?: string | undefined;
  OnLoadConfig?: Uint8Array | undefined;
  AgentId?: string | undefined;
  RequestId?: string | undefined;
  Dev?: boolean | undefined;
}

export interface PluginArtifactLoadResp {
  Status: AppStatus;
  PluginId: string;
  ArtifactHash: string;
  HttpAddr?: string | undefined;
}

export interface PluginArtifactReloadPrepareReq {
  Manifest: AppManifest;
  Abi: PluginAbi;
  ArtifactPath: string;
  ArtifactHash?: string | undefined;
  EntrySymbol?: string | undefined;
  Assets?: Record<string, Uint8Array> | undefined;
  OnLoadConfig?: Uint8Array | undefined;
  AgentId?: string | undefined;
  RequestId?: string | undefined;
}

export interface PluginArtifactReloadPrepareResp {
  Token: string;
  PluginId: string;
  ArtifactHash: string;
  Status: AppStatus;
}

export interface PluginArtifactReloadCommitReq {
  Token: string;
}

export interface PluginArtifactReloadCommitResp {
  Status: AppStatus;
  PluginId: string;
  ArtifactHash: string;
  HttpAddr?: string | undefined;
}

export interface PluginArtifactReloadAbortReq {
  Token: string;
}

export interface PluginArtifactReloadAbortResp {

}

export interface PluginArtifactUnloadReq {
  PluginId: string;
  AgentId?: string | undefined;
  RequestId?: string | undefined;
}

export interface PluginArtifactUnloadResp {
  Removed: number;
}

export interface PluginAssetsPutReq {
  PluginId: string;
  Assets: Record<string, Uint8Array>;
}

export interface PluginAssetsPutResp {
  Registered: number;
}

export interface PluginAssetsRemoveReq {
  PluginId: string;
}

export interface PluginAssetsRemoveResp {
  Removed: number;
}

export interface PluginStateGetReq {
  Plugin: string;
  Key: string;
}

export interface PluginStateGetResp {
  Value: Uint8Array;
  Found?: boolean | undefined;
}

export interface PluginStateSetReq {
  Plugin: string;
  Key: string;
  Value: Uint8Array;
}

export interface PluginStateSetResp {

}

export interface PluginStateDeleteReq {
  Plugin: string;
  Key: string;
}

export interface PluginStateDeleteResp {
  Removed: boolean;
}

export interface PluginStateListReq {
  Plugin: string;
  Prefix?: string | undefined;
}

export interface PluginStateListResp {
  Keys: string[];
}

export interface PluginStateAppendReq {
  Plugin: string;
  Key: string;
  Data: Uint8Array;
}

export interface PluginStateAppendResp {

}

export interface PluginStateGetManyReq {
  Plugin: string;
  Keys: string[];
}

export interface PluginStateGetManyResp {
  Entries: Record<string, Uint8Array>;
}

export interface PluginStateSetManyReq {
  Plugin: string;
  Entries: Record<string, Uint8Array>;
}

export interface PluginStateSetManyResp {

}

export interface PluginStatePurgeReq {
  PluginId: string;
}

export interface PluginStatePurgeResp {
  Removed: boolean;
}

export interface PluginAppDataUsageItem {
  PluginId: string;
  DataDir: string;
  Bytes: number;
  Files: number;
  Loaded?: boolean | undefined;
}

export interface PluginAppDataUsageReq {
  PluginId?: string | undefined;
}

export interface PluginAppDataUsageResp {
  Items: PluginAppDataUsageItem[];
  TotalBytes: number;
  SoftLimitBytes: number;
}
