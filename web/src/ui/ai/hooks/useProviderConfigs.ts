import { useState, useEffect, useCallback, useRef } from 'react'
import { client } from '../../../application/generated-client'
import * as aimanagerProvider from '../../../gen-clients/aimanager/client'
import type { Provider, AIManagerProviderConfigureReq } from '../../../gen-clients/system/types'

export interface UseProviderConfigsResult {
  providers: Provider[]
  loading: boolean
  error: string | null
  reload: (silent?: boolean) => Promise<Provider[]>
  configure: (req: AIManagerProviderConfigureReq) => Promise<Provider[]>
  remove: (name: string) => Promise<Provider[]>
}

export function useProviderConfigs(): UseProviderConfigsResult {
  const [providers, setProviders] = useState<Provider[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const mountedRef = useRef(true)

  const reload = useCallback(async (silent?: boolean) => {
    if (!silent) {
      setLoading(true)
      setError(null)
    }
    let items: Provider[] = []
    try {
      const resp = await aimanagerProvider.providerList(client)
      items = resp.Items
      if (mountedRef.current) {
        setProviders(items)
      }
    } catch (err) {
      if (mountedRef.current && !silent) {
        setError(err instanceof Error ? err.message : String(err))
      }
    } finally {
      if (mountedRef.current && !silent) {
        setLoading(false)
      }
    }
    return items
  }, [])

  useEffect(() => {
    mountedRef.current = true
    void reload()
    return () => { mountedRef.current = false }
  }, [reload])

  // Keep every useProviderConfigs consumer in sync: when one instance mutates
  // providers (configure/remove), it dispatches this event so others reload.
  // Silent: a non-silent reload flips `loading` and unmounts provider lists,
  // which collapses the scroll container and resets the user's scroll position.
  useEffect(() => {
    const handler = () => { void reload(true) }
    window.addEventListener('sporemind:providers-changed', handler)
    return () => window.removeEventListener('sporemind:providers-changed', handler)
  }, [reload])

  const configure = useCallback(async (req: AIManagerProviderConfigureReq) => {
    await aimanagerProvider.providerConfigure(client, req)
    const items = await reload(true)
    window.dispatchEvent(new CustomEvent('sporemind:providers-changed'))
    return items
  }, [reload])

  const remove = useCallback(async (name: string) => {
    await aimanagerProvider.providerConfigure(client, {
      Name: name,
      Kind: '',
      Endpoint: '',
      Models: [],
      AuthToken: '',
    })
    const items = await reload(true)
    window.dispatchEvent(new CustomEvent('sporemind:providers-changed'))
    return items
  }, [reload])

  return { providers, loading, error, reload, configure, remove }
}
