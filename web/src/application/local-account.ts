// Local-instance login state for remote-navigated windows.
//
// When the main window is switched to a remote connection (?server= + ?conn=),
// every data-plane call goes to the REMOTE gateway — but the bottom-left
// account menu must keep showing THIS machine's login state. The Wails host
// bindings (GetGatewayAddr / GetAdminToken) are host-local and ignore ?server=,
// so a plain HTTP POST to the local gateway with a freshly minted admin token
// reaches the local workspace/cloud actors without touching the remote client.
//
// Used only from the account-menu surface; everything else in a remote window
// stays remote on purpose.

import * as desktop from '../bindings/github.com/qomos-w/sporemind/pkg/desktop/app'
import type { AccountSnapshot, CloudAccountLinkReq, CloudAccountStatus } from '../gen-clients/system/types'

let cachedBase: string | null = null

async function localBase(): Promise<string> {
  if (cachedBase) return cachedBase
  const addr = await desktop.GetGatewayAddr()
  if (!addr) throw new Error('local-account: no local gateway address')
  cachedBase = `http://${addr}`
  return cachedBase
}

async function localPost<T>(callID: string, body?: unknown): Promise<T> {
  const [base, token] = await Promise.all([localBase(), desktop.GetAdminToken()])
  const resp = await fetch(`${base}/api/${callID}`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify(body ?? {}),
  })
  if (!resp.ok) {
    const text = await resp.text().catch(() => '')
    throw new Error(`local-account: ${callID} failed (HTTP ${resp.status})${text ? `: ${text.slice(0, 200)}` : ''}`)
  }
  return (await resp.json()) as T
}

/** This machine's account snapshot, for the account menu in remote mode. */
export async function localAccountSnapshot(): Promise<AccountSnapshot> {
  return localPost<AccountSnapshot>('workspace.account')
}

/** This machine's cloud-account link status. */
export async function localCloudStatus(): Promise<CloudAccountStatus> {
  return localPost<CloudAccountStatus>('cloudaccount.status')
}

/** Force an immediate local entitlement re-sync. */
export async function localCloudSync(): Promise<CloudAccountStatus> {
  return localPost<CloudAccountStatus>('cloudaccount.sync')
}

/** Unlink this machine's cloud account. */
export async function localCloudUnlink(): Promise<void> {
  await localPost('cloudaccount.unlink')
}

/** Link a cloud account on THIS machine (cloud OAuth callback in remote mode). */
export async function localCloudLink(req: CloudAccountLinkReq): Promise<CloudAccountStatus> {
  return localPost<CloudAccountStatus>('cloudaccount.link', req)
}

/** Test hook: drop the cached gateway base between tests. */
export function __resetLocalAccountCacheForTests(): void {
  cachedBase = null
}
