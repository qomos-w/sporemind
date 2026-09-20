import type { GosporeClient, InvokeOptions } from '@qomos/gospore-client'
import type {
  LspInstallProgressEvent,
  LspInstallReq,
  LspInstallResp,
  LspStatusReq,
  LspStatusResp,
} from '../../../gen-types/lsp'
import { SchemaIDs } from '../../../gen-types/registry'

/**
 * Thin typed wrapper over the frozen lsp.status / lsp.install wire contract.
 *
 * The generated client in web/src/gen-clients/lsp does not yet export these
 * callables because the backend wiring task is still in progress. Once it
 * lands and `make gen-ts` is re-run, this module should be replaced by the
 * generated helpers. Until then it uses explicit schema IDs derived from the
 * frozen schema contract (3316-3321).
 */

export type LspInstallProgressHandler = (payload: LspInstallProgressEvent) => void

export async function lspStatus(
  client: GosporeClient,
  req: LspStatusReq,
  opts?: InvokeOptions,
): Promise<LspStatusResp> {
  return client.invoke<LspStatusReq, LspStatusResp>('lsp.status', req, {
    reqSchemaId: SchemaIDs.LspStatusReq,
    resSchemaId: SchemaIDs.LspStatusResp,
    ...opts,
  })
}

export async function lspInstall(
  client: GosporeClient,
  req: LspInstallReq,
  opts?: InvokeOptions,
): Promise<LspInstallResp> {
  return client.invoke<LspInstallReq, LspInstallResp>('lsp.install', req, {
    reqSchemaId: SchemaIDs.LspInstallReq,
    resSchemaId: SchemaIDs.LspInstallResp,
    ...opts,
  })
}

export function onLspInstallProgress(
  client: GosporeClient,
  handler: LspInstallProgressHandler,
): () => void {
  return client.events.onService('lspserver', 'lsp.install_progress', (payload) => {
    handler(payload as LspInstallProgressEvent)
  })
}
