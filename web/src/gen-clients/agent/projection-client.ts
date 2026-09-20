// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import { getProjection, watchProjection, Projections } from "../projections";
import type * as systemTypes from "../system/types";

export async function getActiveturnref(client: GosporeClient): Promise<string> {
  return getProjection<string>(client, Projections.agent.ActiveTurnRef);
}

export async function *watchActiveturnref(client: GosporeClient): AsyncIterable<string> {
  yield* watchProjection<string>(client, Projections.agent.ActiveTurnRef);
}

export async function getComponentmounts(client: GosporeClient): Promise<systemTypes.AgentComponentMount[]> {
  return getProjection<systemTypes.AgentComponentMount[]>(client, Projections.agent.ComponentMounts);
}

export async function *watchComponentmounts(client: GosporeClient): AsyncIterable<systemTypes.AgentComponentMount[]> {
  yield* watchProjection<systemTypes.AgentComponentMount[]>(client, Projections.agent.ComponentMounts);
}

export async function getComponentrevision(client: GosporeClient): Promise<number> {
  return getProjection<number>(client, Projections.agent.ComponentRevision);
}

export async function *watchComponentrevision(client: GosporeClient): AsyncIterable<number> {
  yield* watchProjection<number>(client, Projections.agent.ComponentRevision);
}

export async function getDisplayname(client: GosporeClient): Promise<string> {
  return getProjection<string>(client, Projections.agent.DisplayName);
}

export async function *watchDisplayname(client: GosporeClient): AsyncIterable<string> {
  yield* watchProjection<string>(client, Projections.agent.DisplayName);
}

export async function getRawsession(client: GosporeClient): Promise<systemTypes.RawSession> {
  return getProjection<systemTypes.RawSession>(client, Projections.agent.RawSession);
}

export async function *watchRawsession(client: GosporeClient): AsyncIterable<systemTypes.RawSession> {
  yield* watchProjection<systemTypes.RawSession>(client, Projections.agent.RawSession);
}

export async function getSession(client: GosporeClient): Promise<systemTypes.Session> {
  return getProjection<systemTypes.Session>(client, Projections.agent.Session);
}

export async function *watchSession(client: GosporeClient): AsyncIterable<systemTypes.Session> {
  yield* watchProjection<systemTypes.Session>(client, Projections.agent.Session);
}

export async function getTitle(client: GosporeClient): Promise<string> {
  return getProjection<string>(client, Projections.agent.Title);
}

export async function *watchTitle(client: GosporeClient): AsyncIterable<string> {
  yield* watchProjection<string>(client, Projections.agent.Title);
}

export async function getTurnpausekind(client: GosporeClient): Promise<string> {
  return getProjection<string>(client, Projections.agent.TurnPauseKind);
}

export async function *watchTurnpausekind(client: GosporeClient): AsyncIterable<string> {
  yield* watchProjection<string>(client, Projections.agent.TurnPauseKind);
}

export async function getTurnstate(client: GosporeClient): Promise<string> {
  return getProjection<string>(client, Projections.agent.TurnState);
}

export async function *watchTurnstate(client: GosporeClient): AsyncIterable<string> {
  yield* watchProjection<string>(client, Projections.agent.TurnState);
}

