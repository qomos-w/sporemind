// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function document(client: GosporeClient, req: systemTypes.InspectDocumentReq, opts?: InvokeOptions): Promise<systemTypes.InspectDocument> {
  return client.invoke<systemTypes.InspectDocumentReq, systemTypes.InspectDocument>("inspect.document", req, { reqSchemaId: 854, resSchemaId: 861, ...opts });
}

export const document_meta = {
  callable: "inspect.document",
  name: "document",
  reqSchemaId: 854,
  resSchemaId: 861,
} as const;

