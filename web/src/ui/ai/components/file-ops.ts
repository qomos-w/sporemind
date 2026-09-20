import { client } from '../../../application/generated-client'
import * as projectClient from '../../../gen-clients/project/client'

/** Basename of a path (last segment). */
export function baseName(path: string): string {
  if (path === '.' || path === '') return '.'
  const idx = path.lastIndexOf('/')
  return idx < 0 ? path : path.slice(idx + 1)
}

/** Join a parent path with a child name. */
export function joinPath(parent: string, name: string): string {
  if (parent === '.' || parent === '') return name
  return `${parent}/${name}`
}

/** Parent directory of a path. */
export function parentOf(path: string): string {
  if (path === '.' || path === '') return '.'
  const idx = path.lastIndexOf('/')
  if (idx < 0) return '.'
  return path.slice(0, idx) || '.'
}

/** Whether moving sourcePath into targetDir is valid. */
export function canMove(sourcePath: string, targetDir: string): boolean {
  if (!sourcePath || sourcePath === targetDir) return false
  if (targetDir === sourcePath || targetDir.startsWith(sourcePath + '/')) return false
  if (parentOf(sourcePath) === targetDir) return false
  return true
}

/** Drain a streaming shell_exec and return the final exit code. */
export async function runShell(projectId: string, command: string, args: string[], dir?: string, confirm?: boolean): Promise<number> {
  let exitCode = 1
  try {
    for await (const chunk of projectClient.shellExec(
      client,
      { Command: command, Args: args, Dir: dir, Confirm: confirm },
      { target: projectId },
    )) {
      if (chunk.ExitCode != null) exitCode = chunk.ExitCode
    }
  } catch {
    exitCode = 1
  }
  return exitCode
}

export type DesktopPlatform = 'win' | 'mac' | 'linux'

export function detectPlatform(): DesktopPlatform {
  const ua = typeof navigator !== 'undefined'
    ? ((navigator as { userAgentData?: { platform?: string } }).userAgentData?.platform || navigator.platform || '')
    : ''
  const p = ua.toLowerCase()
  if (p.includes('win')) return 'win'
  if (p.includes('mac')) return 'mac'
  return 'linux'
}

/**
 * Resolve the working directory for a file action. Agent turns may live in a
 * worktree whose absolute path sits outside the project root — shell_exec only
 * accepts such absolute dirs with confirm=true.
 */
async function resolveShellDir(projectId: string, worktreeId?: string): Promise<{ dir?: string; confirm?: boolean }> {
  if (!worktreeId) return {}
  try {
    const wt = await projectClient.worktreeGet(client, { WorktreeID: worktreeId }, { target: projectId })
    if (wt.Path) return { dir: wt.Path, confirm: true }
  } catch {
    // worktree no longer exists — fall back to the project root
  }
  return {}
}

/** Open a project-relative file with the OS default application. */
export async function openInSystem(projectId: string, path: string, worktreeId?: string): Promise<boolean> {
  const { dir, confirm } = await resolveShellDir(projectId, worktreeId)
  const platform = detectPlatform()
  let args: string[]
  if (platform === 'win') {
    // MSYS rewrites a single leading slash ("/c") into a path — the double
    // slash survives to cmd.exe as /c. start "" holds the title slot so quoted
    // paths aren't mistaken for the window title.
    args = ['-c', `cmd //c start "" "$(cygpath -w "$PWD/${path}" 2>/dev/null || echo "${path}")"`]
  } else if (platform === 'mac') {
    args = ['-c', `open "$PWD/${path}"`]
  } else {
    args = ['-c', `xdg-open "$PWD/${path}"`]
  }
  const code = await runShell(projectId, 'bash', args, dir, confirm)
  return code === 0
}

/** Reveal a project-relative file in the system file manager (selected). */
export async function revealInSystem(projectId: string, path: string, worktreeId?: string): Promise<boolean> {
  const { dir, confirm } = await resolveShellDir(projectId, worktreeId)
  const platform = detectPlatform()
  let args: string[]
  if (platform === 'win') {
    args = ['-c', `explorer.exe /select,"$(cygpath -w "$PWD/${path}" 2>/dev/null || echo "${path}")"`]
  } else if (platform === 'mac') {
    args = ['-c', `open -R "$PWD/${path}"`]
  } else {
    args = ['-c', `xdg-open "$(dirname "$PWD/${path}")"`]
  }
  const code = await runShell(projectId, 'bash', args, dir, confirm)
  // explorer.exe exits 1 even when the reveal succeeded.
  return code === 0 || (platform === 'win' && code === 1)
}

/** Paste (copy or move) a clipboard entry into a target directory. */
export async function pasteIntoDir(
  projectId: string,
  clip: { path: string; isCut: boolean },
  targetDir: string,
): Promise<{ ok: boolean; conflict: boolean }> {
  if (clip.isCut) {
    if (!canMove(clip.path, targetDir)) return { ok: false, conflict: false }
  } else {
    if (targetDir === clip.path || targetDir.startsWith(clip.path + '/')) {
      return { ok: false, conflict: false }
    }
  }
  const dest = joinPath(targetDir, baseName(clip.path))
  const existsCode = await runShell(projectId, 'test', ['-e', dest])
  if (existsCode === 0) return { ok: false, conflict: true }
  if (clip.isCut) {
    const code = await runShell(projectId, 'mv', [clip.path, dest])
    if (code !== 0) return { ok: false, conflict: false }
  } else {
    const code = await runShell(projectId, 'cp', ['-r', clip.path, dest])
    if (code !== 0) return { ok: false, conflict: false }
  }
  return { ok: true, conflict: false }
}

/** Move a file/directory from source to a target directory (drag & drop). */
export async function moveIntoDir(
  projectId: string,
  source: string,
  targetDir: string,
): Promise<boolean> {
  if (!canMove(source, targetDir)) return false
  const dest = joinPath(targetDir, baseName(source))
  const code = await runShell(projectId, 'mv', [source, dest])
  return code === 0
}