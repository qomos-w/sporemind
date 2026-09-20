// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface PuppetNode {
  Guid: string;
  Name: string;
  Kind: string;
  Enabled: boolean;
  Z?: number | undefined;
  TextureAssetId?: string | undefined;
  Mesh?: PuppetMesh | undefined;
  Params?: Record<string, string> | undefined;
  Children: PuppetNode[];
}

export interface PuppetDocument {
  Id: string;
  Revision: number;
  Name: string;
  SourcePath?: string | undefined;
  Root: PuppetNode;
  Params?: PuppetParam[] | undefined;
  ParamValues?: Record<string, string> | undefined;
  Bindings?: PuppetParamBinding[] | undefined;
  CreatedAt: string;
  UpdatedAt: string;
}

export interface PuppetDocumentSnapshot {
  Document: PuppetDocument;
  StagedAssetCount: number;
  RevisionCount: number;
}

export interface PuppetDocumentSnapshotReq {

}

export interface PuppetDocumentSnapshotResp {
  Snapshot: PuppetDocumentSnapshot;
}

export interface PuppetStagedAsset {
  Id: string;
  DocumentId: string;
  State: string;
  Kind: string;
  DataRef: string;
  Name?: string | undefined;
  Width?: number | undefined;
  Height?: number | undefined;
  SourceRef?: string | undefined;
  GenParamsSummary?: string | undefined;
  NodeGuid?: string | undefined;
  CreatedAt: string;
  ReviewedAt?: string | undefined;
  ReviewReason?: string | undefined;
}

export interface PuppetAssetListReq {
  State?: string | undefined;
}

export interface PuppetAssetListResp {
  Assets: PuppetStagedAsset[];
}

export interface PuppetAssetStageReq {
  Kind: string;
  DataRef: string;
  Name?: string | undefined;
  Width?: number | undefined;
  Height?: number | undefined;
  SourceRef?: string | undefined;
  GenParamsSummary?: string | undefined;
}

export interface PuppetAssetStageResp {
  Asset: PuppetStagedAsset;
}

export interface PuppetAssetCommitReq {
  AssetId: string;
  NodeGuid: string;
}

export interface PuppetAssetCommitResp {
  Asset: PuppetStagedAsset;
  Revision: PuppetRevision;
}

export interface PuppetAssetRejectReq {
  AssetId: string;
  Reason?: string | undefined;
}

export interface PuppetAssetRejectResp {
  Asset: PuppetStagedAsset;
}

export interface PuppetRevision {
  Id: string;
  DocumentId: string;
  Sequence: number;
  ParentRevisionId: string;
  Author: string;
  CommandKind: string;
  AuditSource?: string | undefined;
  Summary?: string | undefined;
  AffectedGuids?: string[] | undefined;
  Timestamp: string;
}

export interface PuppetRevisionLogReq {
  Limit?: number | undefined;
}

export interface PuppetRevisionLogResp {
  Revisions: PuppetRevision[];
}

export interface PuppetEditCommand {
  Kind: string;
  TargetGuid: string;
  Author: string;
  Params?: Record<string, string> | undefined;
  RequestId?: string | undefined;
}

export interface PuppetEditReq {
  Command: PuppetEditCommand;
}

export interface PuppetEditResp {
  Applied: boolean;
  Revision: PuppetRevision;
  Detail?: string | undefined;
  AffectedGuids?: string[] | undefined;
}

export interface PuppetAgentRequest {
  TargetGuid: string;
  RequestId: string;
  AuditSource: string;
}

export interface PuppetAgentSnapshotReq {
  Envelope: PuppetAgentRequest;
}

export interface PuppetAgentSnapshotResp {
  Snapshot: PuppetDocumentSnapshot;
}

export interface PuppetAgentRevisionLogReq {
  Envelope: PuppetAgentRequest;
  Limit?: number | undefined;
}

export interface PuppetAgentRevisionLogResp {
  Revisions: PuppetRevision[];
}

export interface PuppetAgentAssetListReq {
  Envelope: PuppetAgentRequest;
  State?: string | undefined;
}

export interface PuppetAgentAssetListResp {
  Assets: PuppetStagedAsset[];
}

export interface PuppetAgentEditReq {
  Envelope: PuppetAgentRequest;
  Command: PuppetEditCommand;
}

export interface PuppetAgentEditResp {
  Result: PuppetEditResp;
}

export interface PuppetAgentAssetStageReq {
  Envelope: PuppetAgentRequest;
  Operation: PuppetAssetStageReq;
}

export interface PuppetAgentAssetStageResp {
  Result: PuppetAssetStageResp;
}

export interface PuppetAgentAssetRejectReq {
  Envelope: PuppetAgentRequest;
  AssetId: string;
  Reason?: string | undefined;
}

export interface PuppetAgentAssetRejectResp {
  Result: PuppetAssetRejectResp;
}

export interface PuppetMesh {
  Vertices?: number[] | undefined;
  Indices?: number[] | undefined;
  UVs?: number[] | undefined;
}

export interface PuppetGenerateNodeMeshReq {
  NodeGuid: string;
  Method?: string | undefined;
  SampleDensity?: number | undefined;
  RequestId?: string | undefined;
}

export interface PuppetGenerateNodeMeshResp {
  Applied: boolean;
  NodeGuid: string;
  Mesh: PuppetMesh;
  Revision: PuppetRevision;
  Detail?: string | undefined;
}

export interface PuppetAssetReadReq {
  AssetId: string;
}

export interface PuppetAssetReadResp {
  AssetId: string;
  Uri: string;
  MimeType?: string | undefined;
  Width?: number | undefined;
  Height?: number | undefined;
}

export interface PuppetDocumentRevertToReq {
  RevisionId: string;
}

export interface PuppetDocumentRevertToResp {
  Applied: boolean;
  Document: PuppetDocument;
  Revision: PuppetRevision;
  Detail?: string | undefined;
}

export interface PuppetParam {
  Id: string;
  Name: string;
  Kind: string;
  Default: string;
  Min?: string | undefined;
  Max?: string | undefined;
  Step?: string | undefined;
  Group?: string | undefined;
  NoInherit?: boolean | undefined;
}

export interface PuppetParamBinding {
  ParamId: string;
  NodeGuid: string;
  Property: string;
  Multiplier?: number | undefined;
  Offset?: number | undefined;
}

export interface PuppetParamBindingReq {
  Action: string;
  Binding: PuppetParamBinding;
  RequestId?: string | undefined;
}

export interface PuppetParamBindingResp {
  Applied: boolean;
  Revision: PuppetRevision;
  Detail?: string | undefined;
}

export interface PuppetParamListReq {

}

export interface PuppetParamListResp {
  Params: PuppetParam[];
  Bindings: PuppetParamBinding[];
}

export interface PuppetAgentExploreQueryReq {
  Envelope: PuppetAgentRequest;
  Kind?: string | undefined;
  NamePattern?: string | undefined;
  HasTexture?: boolean | undefined;
  HasMesh?: boolean | undefined;
}

export interface PuppetNodeInfo {
  Guid: string;
  Name: string;
  Kind: string;
  Enabled: boolean;
  Z?: number | undefined;
  TextureAssetId?: string | undefined;
  HasMesh?: boolean | undefined;
  Depth?: number | undefined;
  ParentGuid?: string | undefined;
}

export interface PuppetAgentExploreQueryResp {
  Nodes: PuppetNodeInfo[];
}

export interface PuppetViewportCaptureReq {
  Width?: number | undefined;
  Height?: number | undefined;
  Format?: string | undefined;
  Quality?: number | undefined;
}

export interface PuppetViewportCaptureResp {
  Uri: string;
  Width: number;
  Height: number;
  MimeType?: string | undefined;
  Source?: string | undefined;
}

export interface PuppetAgentGenerateNodeMeshReq {
  Envelope: PuppetAgentRequest;
  Operation: PuppetGenerateNodeMeshReq;
}

export interface PuppetAgentGenerateNodeMeshResp {
  Result: PuppetGenerateNodeMeshResp;
}

export interface PuppetAgentAssetReadReq {
  Envelope: PuppetAgentRequest;
  AssetId: string;
}

export interface PuppetAgentAssetReadResp {
  Result: PuppetAssetReadResp;
}

export interface PuppetAgentRevertToReq {
  Envelope: PuppetAgentRequest;
  RevisionId: string;
}

export interface PuppetAgentRevertToResp {
  Result: PuppetDocumentRevertToResp;
}

export interface PuppetAgentExploreCaptureReq {
  Envelope: PuppetAgentRequest;
  Width?: number | undefined;
  Height?: number | undefined;
  Format?: string | undefined;
}

export interface PuppetAgentExploreCaptureResp {
  Result: PuppetViewportCaptureResp;
}
