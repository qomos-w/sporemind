import { useEffect, useState } from 'react'
import { client } from '../../../application/generated-client'
import * as projectClient from '../../../gen-clients/project/client'

// Module-level cache + in-flight dedup: the project new-chat page and the
// shell welcome row can both mount for the same project; sharing one fetch
// avoids issuing no_git_mode_get twice for the same projectId.
const gitRepoCache = new Map<string, boolean>()
const inflight = new Map<string, Promise<boolean>>()

export function useHasGitRepo(projectId?: string): { hasGitRepo: boolean | undefined; loading: boolean } {
  const [state, setState] = useState<{ hasGitRepo: boolean | undefined; loading: boolean }>(() => {
    if (!projectId) return { hasGitRepo: undefined, loading: false }
    const cached = gitRepoCache.get(projectId)
    return cached === undefined
      ? { hasGitRepo: undefined, loading: true }
      : { hasGitRepo: cached, loading: false }
  })

  useEffect(() => {
    if (!projectId) {
      setState({ hasGitRepo: undefined, loading: false })
      return
    }
    if (gitRepoCache.has(projectId)) {
      setState({ hasGitRepo: gitRepoCache.get(projectId), loading: false })
      return
    }
    let cancelled = false
    setState({ hasGitRepo: undefined, loading: true })
    let promise = inflight.get(projectId)
    if (!promise) {
      promise = projectClient.noGitModeGet(client, {}, { target: projectId }).then(resp => resp.HasGitRepo)
      inflight.set(projectId, promise)
    }
    promise
      .then(hasGitRepo => {
        gitRepoCache.set(projectId, hasGitRepo)
        if (!cancelled) setState({ hasGitRepo, loading: false })
      })
      .catch(() => {
        if (!cancelled) setState({ hasGitRepo: undefined, loading: false })
      })
      .finally(() => {
        inflight.delete(projectId)
      })
    return () => { cancelled = true }
  }, [projectId])

  return state
}
