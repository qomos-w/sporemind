// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import { getProjection, watchProjection, Projections } from "../projections";
import type * as systemTypes from "../system/types";

export async function getSnapshot(client: GosporeClient): Promise<systemTypes.WorkbenchSnapshot> {
  return getProjection<systemTypes.WorkbenchSnapshot>(client, Projections.workbench.Snapshot);
}

export async function *watchSnapshot(client: GosporeClient): AsyncIterable<systemTypes.WorkbenchSnapshot> {
  yield* watchProjection<systemTypes.WorkbenchSnapshot>(client, Projections.workbench.Snapshot);
}

