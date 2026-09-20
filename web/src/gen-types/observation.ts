// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface CallableParam {
  Name: string;
  Type: string;
  Description?: string | undefined;
  Required?: boolean | undefined;
}

export interface CallableInterface {
  Name: string;
  Kind: string;
  Params?: CallableParam[] | undefined;
  FinalType?: string | undefined;
  FinalFields?: CallableParam[] | undefined;
  NextType?: string | undefined;
  NextFields?: CallableParam[] | undefined;
  ReqType?: string | undefined;
  FinalDesc?: string | undefined;
  ChunkDesc?: string | undefined;
  HasError?: boolean | undefined;
  Stream?: boolean | undefined;
  Permission?: string | undefined;
  Description?: string | undefined;
  Category?: string | undefined;
  EffectKind?: string | undefined;
  ServiceName?: string | undefined;
  ToolName?: string | undefined;
  ReqSchemaID?: number | undefined;
  FinalSchemaID?: number | undefined;
}

export interface TopologyNodeDescriptor {
  CapabilityId?: string | undefined;
  CapabilityName?: string | undefined;
  CapabilityKind?: string | undefined;
  DisplayName?: string | undefined;
  Badge?: string | undefined;
  Sublabel?: string | undefined;
}

export interface TopologyCardRow {
  Label: string;
  Value: string;
  Mono?: boolean | undefined;
  Truncate?: boolean | undefined;
  Badge?: string | undefined;
  ExpandedRows?: TopologyCardRow[] | undefined;
  Tooltip?: string | undefined;
}

export interface TopologyCardSection {
  Key: string;
  Title: string;
  Rows: TopologyCardRow[];
}

export interface TopologyCard {
  Label?: string | undefined;
  Badge?: string | undefined;
  Sublabel?: string | undefined;
  DetailSections?: TopologyCardSection[] | undefined;
}

export interface UnifiedGraphEdge {
  Id: string;
  From: string;
  To: string;
  Kind: string;
  Label?: string | undefined;
  LifecycleScope?: string | undefined;
  Status?: string | undefined;
}

export interface UnifiedGraphNode {
  Id: string;
  Kind: string;
  Label: string;
  ActorType?: string | undefined;
  ActorId?: string | undefined;
  StepKind?: string | undefined;
  Strategy?: string | undefined;
  Status: string;
  ParentId?: string | undefined;
  ContainerId?: string | undefined;
  Collapsible?: boolean | undefined;
  Expanded?: boolean | undefined;
  Detail?: Record<string, unknown> | undefined;
  Interfaces?: CallableInterface[] | undefined;
  Descriptor?: TopologyNodeDescriptor | undefined;
  Card?: TopologyCard | undefined;
}

export interface UnifiedGraph {
  Version: string;
  Nodes: UnifiedGraphNode[];
  Edges: UnifiedGraphEdge[];
}

export interface GraphPatch {
  Epoch: number;
  Timestamp: string;
  AddedNodes: UnifiedGraphNode[];
  RemovedNodes: string[];
  UpdatedNodes: UnifiedGraphNode[];
  AddedEdges: UnifiedGraphEdge[];
  RemovedEdges: string[];
}

export interface TopologySyncReq {
  ClientEpoch: number;
}

export interface TopologySyncResp {
  CurrentEpoch: number;
  Snapshot?: UnifiedGraph | undefined;
  Patches: GraphPatch[];
}

export interface TopologyHistoryEntry {
  Epoch: number;
  Timestamp: string;
  ChangeCount: number;
}

export interface TopologyHistoryResp {
  Entries: TopologyHistoryEntry[];
}

export interface TopologyEpochEvent {
  Epoch: number;
  Patch?: GraphPatch | undefined;
}

export interface ServiceInfo {
  Name: string;
  ActorId: string;
  ActorType: string;
}

export interface RuntimeListServicesReq {

}

export interface RuntimeListServicesResp {
  Items: ServiceInfo[];
}

export interface RuntimeBuildInfoReq {

}

export interface RuntimeBuildInfoResp {
  Version: string;
  BuildType?: string | undefined;
  BuildFlavor?: string | undefined;
  Commit?: string | undefined;
  BuildTime?: string | undefined;
  Dirty?: boolean | undefined;
  PublicVersion?: string | undefined;
  Channel?: string | undefined;
}
