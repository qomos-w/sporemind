import { client } from './generated-client'
import * as appmanagerClient from '../gen-clients/appmanager/client'

/**
 * Execute an app command entrypoint by invoking the real AppManager callable.
 *
 * Command entrypoints have no payload by design; the command ID maps to the
 * callable declared in the app manifest.
 */
export async function executeAppCommand(appID: string, commandID: string): Promise<Uint8Array> {
  const resp = await appmanagerClient.invoke(client, {
    Id: appID,
    Callable: commandID,
    Payload: new Uint8Array(),
  })
  return resp.Payload ?? new Uint8Array()
}
