import { client } from '../../application/generated-client'
import * as inspect from '../../gen-clients/inspect/client'
import type { InspectRef, InspectDocument, InspectDocumentReq } from '../../gen-clients/system/types'

export async function fetchInspectorDocument(ref: InspectRef): Promise<InspectDocument> {
  const req: InspectDocumentReq = {
    Kind: ref.Kind,
    Id: ref.Id,
    Scope: ref.Scope ?? {},
  }
  return inspect.document(client, req)
}
