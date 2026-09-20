// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface MemorySaveReq {
  Content: string;
  Layer?: string | undefined;
}

export interface MemorySaveResp {
  Node: MemoryNode;
}

export interface MemoryRecallReq {
  Query?: string | undefined;
  Layer?: string | undefined;
  Limit?: number | undefined;
}

export interface MemoryRecallResp {
  Nodes: MemoryNode[];
}

export interface MemoryWeaveApplyReq {
  NewNodeId: string;
  Decision: string;
  TargetNodeId?: string | undefined;
}

export interface MemorySnapshotReq {

}

export interface MemorySnapshotResp {
  Mounted: boolean;
  SleepDue: boolean;
  Nodes: MemoryNode[];
  Edges: MemoryEdge[];
}

export interface MemoryNode {
  Id: string;
  Layer: string;
  Content: string;
  Head: string;
  Tokens: number;
  Energy: number;
  CreatedAt: string;
  AccessedAt: string;
  MarkedForDeath: boolean;
  Protected: boolean;
  LastAccessTick: number;
}

export interface MemoryEdge {
  From: string;
  To: string;
  Type: string;
}
