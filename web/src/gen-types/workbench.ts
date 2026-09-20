// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { ComponentVisual } from './component';

export interface WorkbenchCardState {
  Id: string;
  Kind: string;
  Title: string;
  Icon: string;
  Color?: string | undefined;
  Score: number;
  Slot: string;
  Pinned: boolean;
  Hidden?: boolean | undefined;
  StatusText?: string | undefined;
  Why?: string | undefined;
  Visual?: ComponentVisual | undefined;
  Mode?: string | undefined;
}

export interface WorkbenchSnapshot {
  Cards: WorkbenchCardState[];
  Generation: number;
  Frozen?: boolean | undefined;
  Maximized?: string | undefined;
}

export interface WorkbenchSnapshotReq {
  IncludeHidden?: boolean | undefined;
}

export interface WorkbenchCardRefReq {
  Id: string;
}

export interface WorkbenchSetHiddenReq {
  Id: string;
  Hidden: boolean;
}

export interface WorkbenchSetPinnedReq {
  Id: string;
  Pinned: boolean;
}

export interface WorkbenchUpsertCardReq {
  Id: string;
  Kind: string;
  Title?: string | undefined;
  Icon?: string | undefined;
  Color?: string | undefined;
  StatusText?: string | undefined;
  Visual?: ComponentVisual | undefined;
  Hidden?: boolean | undefined;
  Score?: number | undefined;
  Mode?: string | undefined;
}

export interface WorkbenchSetFrozenReq {
  Frozen: boolean;
}

export interface WorkbenchSetMaximizedReq {
  Id: string;
  Reason?: string | undefined;
}
