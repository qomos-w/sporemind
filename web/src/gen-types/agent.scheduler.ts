// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { ActiveSchedulerEntry } from './aigen.part4';

export interface AgentSchedulerBindReq {
  SchedulerCardID: string;
  SchedulerName?: string | undefined;
}

export interface AgentSchedulerBindResp {
  ActiveScheduler: ActiveSchedulerEntry[];
}

export interface AgentSchedulerUnbindReq {
  SchedulerCardID: string;
}

export interface AgentSchedulerUnbindResp {
  ActiveScheduler: ActiveSchedulerEntry[];
}
