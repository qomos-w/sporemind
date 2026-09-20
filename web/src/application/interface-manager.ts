import { client } from './generated-client'
import * as interfaceClient from '../gen-clients/interfacemanager/client'
import type { InterfaceManagerEvent, TutorialSpec } from '../gen-types/interfacemanager'

export function onInterfaceManagerEvent(handler: (e: InterfaceManagerEvent) => void): () => void {
  return interfaceClient.OnInterfaceManagerEvent(client, handler)
}

/**
 * Fetch the agent-created dynamic tutorials from the interfacemanager actor.
 * Startup re-sync for the frontend in-memory projection; failures degrade to
 * an empty list (dynamic tutorials simply don't appear until the next sync).
 */
export async function fetchTutorialCatalog(): Promise<TutorialSpec[]> {
  try {
    const resp = await interfaceClient.control(client, { Action: 'tutorial_catalog' })
    return resp.Tutorials ?? []
  } catch (err) {
    console.warn('[interface-manager] tutorial_catalog failed:', err)
    return []
  }
}
