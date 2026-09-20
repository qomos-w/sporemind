// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import { getProjection, watchProjection, Projections } from "../projections";
import type * as systemTypes from "../system/types";

export async function getPlugins(client: GosporeClient): Promise<systemTypes.PluginDescriptor[]> {
  return getProjection<systemTypes.PluginDescriptor[]>(client, Projections.pluginhost.Plugins);
}

export async function *watchPlugins(client: GosporeClient): AsyncIterable<systemTypes.PluginDescriptor[]> {
  yield* watchProjection<systemTypes.PluginDescriptor[]>(client, Projections.pluginhost.Plugins);
}

