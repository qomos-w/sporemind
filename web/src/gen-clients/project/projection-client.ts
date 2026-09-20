// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import { getProjection, watchProjection, Projections } from "../projections";

export async function getGraphs(client: GosporeClient): Promise<Record<string, any>> {
  return getProjection<Record<string, any>>(client, Projections.project.Graphs);
}

export async function *watchGraphs(client: GosporeClient): AsyncIterable<Record<string, any>> {
  yield* watchProjection<Record<string, any>>(client, Projections.project.Graphs);
}

