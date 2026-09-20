import { gitStore } from '../../panels/git-store'

/** CustomEvent the shell listens for to switch the content-mode panel. */
export const SHELL_CONTENT_MODE_EVENT = 'sporemind:set-shell-content-mode'

/** Fallback wait for the git refresh kicked off by setContext. */
export const GIT_JUMP_FALLBACK_TIMEOUT_MS = 5000

/**
 * Jump the shell to git mode showing the given agent's worktree with its HEAD
 * commit selected. This is safe while the agent is mid-turn: it only drives
 * the workspace git store (project-actor path), never the agent actor, so
 * nothing can block on a busy agent. Shared by both badge surfaces
 * (AgentAvatarBar dropdown + AIShellSidebar session row).
 */
export async function jumpToWorktreeGit(projectId: string, worktreeId: string | null | undefined): Promise<void> {
  if (!projectId || !worktreeId) return

  window.dispatchEvent(new CustomEvent(SHELL_CONTENT_MODE_EVENT, { detail: 'git' }))
  gitStore.setContext({ projectId, worktreeId })

  // setContext auto-refreshes; wait until it settles. Fall back after a
  // timeout in case the context was already active (no refresh fired).
  await waitForGitLoad(GIT_JUMP_FALLBACK_TIMEOUT_MS)

  const head = gitStore.state.commits[0]
  if (head) {
    await gitStore.selectCommit(head)
  }
}

function waitForGitLoad(timeoutMs: number): Promise<void> {
  return new Promise((resolve) => {
    if (!gitStore.state.loading) {
      resolve()
      return
    }
    let unsubscribe: () => void = () => {}
    const timer = setTimeout(() => {
      unsubscribe()
      resolve()
    }, timeoutMs)
    unsubscribe = gitStore.subscribe(() => {
      if (!gitStore.state.loading) {
        clearTimeout(timer)
        unsubscribe()
        resolve()
      }
    })
  })
}
