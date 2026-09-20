// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import { getProjection, watchProjection, Projections } from "../projections";
import type * as systemTypes from "../system/types";

export async function getHosts(client: GosporeClient): Promise<systemTypes.SshHost[]> {
  return getProjection<systemTypes.SshHost[]>(client, Projections.sshmanager.Hosts);
}

export async function *watchHosts(client: GosporeClient): AsyncIterable<systemTypes.SshHost[]> {
  yield* watchProjection<systemTypes.SshHost[]>(client, Projections.sshmanager.Hosts);
}

