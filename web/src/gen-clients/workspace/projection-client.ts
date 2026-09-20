// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import { getProjection, watchProjection, Projections } from "../projections";
import type * as systemTypes from "../system/types";

export async function getAgentkindconfigs(client: GosporeClient): Promise<systemTypes.AgentKindConfig[]> {
  return getProjection<systemTypes.AgentKindConfig[]>(client, Projections.workspace.AgentKindConfigs);
}

export async function *watchAgentkindconfigs(client: GosporeClient): AsyncIterable<systemTypes.AgentKindConfig[]> {
  yield* watchProjection<systemTypes.AgentKindConfig[]>(client, Projections.workspace.AgentKindConfigs);
}

export async function getAgents(client: GosporeClient): Promise<systemTypes.AgentRef[]> {
  return getProjection<systemTypes.AgentRef[]>(client, Projections.workspace.Agents);
}

export async function *watchAgents(client: GosporeClient): AsyncIterable<systemTypes.AgentRef[]> {
  yield* watchProjection<systemTypes.AgentRef[]>(client, Projections.workspace.Agents);
}

export async function getMounts(client: GosporeClient): Promise<systemTypes.ProjectRef[]> {
  return getProjection<systemTypes.ProjectRef[]>(client, Projections.workspace.Mounts);
}

export async function *watchMounts(client: GosporeClient): AsyncIterable<systemTypes.ProjectRef[]> {
  yield* watchProjection<systemTypes.ProjectRef[]>(client, Projections.workspace.Mounts);
}

export async function getUi(client: GosporeClient): Promise<systemTypes.WorkspaceUIModel> {
  return getProjection<systemTypes.WorkspaceUIModel>(client, Projections.workspace.UI);
}

export async function *watchUi(client: GosporeClient): AsyncIterable<systemTypes.WorkspaceUIModel> {
  yield* watchProjection<systemTypes.WorkspaceUIModel>(client, Projections.workspace.UI);
}

