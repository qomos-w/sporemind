// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { MonoCardListItem } from './project.wiki.part1';
import { ProjectSpawnAgentReq } from './workspace.part1';

export interface WikiCreateMapReq {
  Id: string;
  Destination?: string | undefined;
  Notes?: string | undefined;
}

export interface WikiCreateMapResp {
  Card: MonoCardListItem;
}

export interface WikiCreateTaskCardReq {
  MapId: string;
  Title: string;
  Question: string;
  DependsOn?: string[] | undefined;
  Status?: string | undefined;
  Category?: string | undefined;
  Bindings?: TaskDataBinding[] | undefined;
  Exec?: Record<string, unknown> | undefined;
}

export interface WikiCreateTaskCardResp {
  Card: MonoCardListItem;
}

export interface WikiSetTaskDependenciesReq {
  MapId: string;
  TaskId: string;
  DependsOn: string[];
  Bindings?: TaskDataBinding[] | undefined;
}

export interface WikiSetTaskDependenciesResp {
  Revision: string;
}

export interface WikiClaimAndSpawnAgentReq {
  CardId: string;
  ExpectedStatuses: string[];
  Spawn: ProjectSpawnAgentReq;
}

export interface WikiClaimAndSpawnAgentResp {
  ActorId: string;
  PreviousStatus: string;
  Raw: string;
}

export interface WikiListDependenciesReq {
  MapId?: string | undefined;
}

export interface WikiListDependenciesResp {
  Edges: TaskDepEdge[];
}

export interface TaskDepEdge {
  MapId: string;
  From: string;
  To: string;
}

export interface TaskDataBinding {
  Input: string;
  FromNode: string;
  FromOutput: string;
}

export interface WikiSetTaskOutputsReq {
  CardId: string;
  Outputs: Record<string, unknown>;
}

export interface WikiSetTaskOutputsResp {
  Card: MonoCardListItem;
}

export interface WikiTemplateSaveReq {
  MapId: string;
  TemplateId: string;
}

export interface WikiTemplateSaveResp {
  TemplateMap: MonoCardListItem;
  NodeMap: TemplateNodeMapping[];
}

export interface WikiTemplateInstantiateReq {
  TemplateMapId: string;
  InstanceMapId: string;
  Inputs?: Record<string, unknown> | undefined;
  Source?: string | undefined;
  SchedulerCardId?: string | undefined;
}

export interface WikiTemplateInstantiateResp {
  InstanceMap: MonoCardListItem;
  NodeMap: TemplateNodeMapping[];
}

export interface TemplateNodeMapping {
  From: string;
  To: string;
}

export interface WikiSetMapInputsReq {
  MapId: string;
  Inputs: Record<string, unknown>;
}

export interface WikiSetMapInputsResp {
  Revision: string;
}

export interface WikiPromoteNodeOutputsReq {
  MapId: string;
  NodeId: string;
}

export interface WikiPromoteNodeOutputsResp {
  Revision: string;
  Inputs: Record<string, unknown>;
}

export interface WikiAutomationBindReq {
  MapId: string;
  SchedulerCardId: string;
}

export interface WikiAutomationBindResp {
  TemplateId: string;
  TemplateMap: MonoCardListItem;
  SchedulerCard: MonoCardListItem;
}

export interface WikiListTemplatesReq {

}

export interface WikiListTemplatesResp {
  Templates: MonoCardListItem[];
}

export interface ProjectWikiUnbindAgentReq {
  AgentActorID: string;
}
