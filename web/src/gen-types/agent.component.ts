// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { AgentComponentMount, AgentComponentSnapshot } from './component';

export interface AgentComponentMountReq {
  CardId: string;
  Enabled?: boolean | undefined;
  Order?: number | undefined;
  Scope?: string | undefined;
}

export interface AgentComponentMountResp {
  Mount: AgentComponentMount;
}

export interface AgentComponentUnmountReq {
  CardId: string;
  Confirm?: boolean | undefined;
}

export interface AgentComponentUnmountResp {
  CardId: string;
}

export interface AgentComponentSetEnabledReq {
  CardId: string;
  Enabled: boolean;
}

export interface AgentComponentSetEnabledResp {
  Mount: AgentComponentMount;
}

export interface AgentComponentListReq {

}

export interface AgentComponentListResp {
  Items: AgentComponentMount[];
}

export interface AgentComponentSnapshotReq {

}

export interface AgentComponentSnapshotResp {
  Snapshot: AgentComponentSnapshot;
}
