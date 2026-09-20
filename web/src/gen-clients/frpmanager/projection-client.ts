// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import { getProjection, watchProjection, Projections } from "../projections";
import type * as systemTypes from "../system/types";

export async function getInstances(client: GosporeClient): Promise<systemTypes.FrpInstanceConfig[]> {
  return getProjection<systemTypes.FrpInstanceConfig[]>(client, Projections.frpmanager.Instances);
}

export async function *watchInstances(client: GosporeClient): AsyncIterable<systemTypes.FrpInstanceConfig[]> {
  yield* watchProjection<systemTypes.FrpInstanceConfig[]>(client, Projections.frpmanager.Instances);
}

