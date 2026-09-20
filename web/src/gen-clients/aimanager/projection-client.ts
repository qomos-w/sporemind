// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import { getProjection, watchProjection, Projections } from "../projections";
import type * as systemTypes from "../system/types";

export async function getAggregatornames(client: GosporeClient): Promise<Record<string, any>> {
  return getProjection<Record<string, any>>(client, Projections.aimanager.AggregatorNames);
}

export async function *watchAggregatornames(client: GosporeClient): AsyncIterable<Record<string, any>> {
  yield* watchProjection<Record<string, any>>(client, Projections.aimanager.AggregatorNames);
}

export async function getProviders(client: GosporeClient): Promise<systemTypes.Provider[]> {
  return getProjection<systemTypes.Provider[]>(client, Projections.aimanager.Providers);
}

export async function *watchProviders(client: GosporeClient): AsyncIterable<systemTypes.Provider[]> {
  yield* watchProjection<systemTypes.Provider[]>(client, Projections.aimanager.Providers);
}

