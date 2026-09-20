// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import { getProjection, watchProjection, Projections } from "../projections";
import type * as systemTypes from "../system/types";

export async function getInstances(client: GosporeClient): Promise<systemTypes.BrowserInstanceConfig[]> {
  return getProjection<systemTypes.BrowserInstanceConfig[]>(client, Projections.browsermanager.Instances);
}

export async function *watchInstances(client: GosporeClient): AsyncIterable<systemTypes.BrowserInstanceConfig[]> {
  yield* watchProjection<systemTypes.BrowserInstanceConfig[]>(client, Projections.browsermanager.Instances);
}

