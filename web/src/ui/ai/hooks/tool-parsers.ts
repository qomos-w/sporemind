import type { FileSystemEditHunk, FileSystemEditResp } from '../../../gen-types/filesystem'
import type { FileChangeEntry, SnapshotRef } from '../model/frame-types'

/** Detect a backend-emitted snapshot reference in a tool_result text.
 *  The backend replaces large project.read outputs with a
 *  `{"__snapshotRef":{id,path,size,startLine,numLines,totalLines[,offset,limit]}}` placeholder so the
 *  streaming step payload stays small. The full content is still
 *  delivered to the LLM history and to the frontend via
 *  agent.read_snapshot. Returns the parsed ref, or undefined if the
 *  text is not a snapshot placeholder. */
export function parseSnapshotRef(text: string | undefined): SnapshotRef | undefined {
  if (!text || !text.includes('__snapshotRef')) return undefined
  try {
    const parsed = JSON.parse(text) as { __snapshotRef?: SnapshotRef }
    if (parsed?.__snapshotRef?.id) return parsed.__snapshotRef
  } catch { /* not a snapshot ref */ }
  return undefined
}

export function parseReadBase64Path(input: string): string | undefined {
  try {
    const parsed: unknown = JSON.parse(input)
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      const path = (parsed as Record<string, unknown>).path
      if (typeof path === 'string' && path.length > 0) return path
    }
  } catch {
    // not valid JSON
  }
  return undefined
}

export function approxBase64ByteSize(text: string): number {
  const padding = text.endsWith('==') ? 2 : text.endsWith('=') ? 1 : 0
  return Math.max(0, Math.floor((text.length * 3) / 4) - padding)
}

export function parseEditOutput(text: string): FileSystemEditResp | null {
  try {
    const parsed: unknown = JSON.parse(text)
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      const r = parsed as Record<string, unknown>
      const hunks: FileSystemEditHunk[] = []
      if (Array.isArray(r.Hunks)) {
        for (const h of r.Hunks) {
          if (h && typeof h === 'object') {
            const hh = h as Record<string, unknown>
            hunks.push({
              Old_start: typeof hh.Old_start === 'number' ? hh.Old_start : 0,
              Old_lines: typeof hh.Old_lines === 'number' ? hh.Old_lines : 0,
              New_start: typeof hh.New_start === 'number' ? hh.New_start : 0,
              New_lines: typeof hh.New_lines === 'number' ? hh.New_lines : 0,
              Lines: Array.isArray(hh.Lines) ? hh.Lines.filter((l): l is string => typeof l === 'string') : [],
            })
          }
        }
      }
      return {
        Replacements: typeof r.Replacements === 'number' ? r.Replacements : 0,
        Additions: typeof r.Additions === 'number' ? r.Additions : 0,
        Deletions: typeof r.Deletions === 'number' ? r.Deletions : 0,
        Hunks: hunks,
      }
    }
  } catch {
    // not valid JSON
  }
  return null
}

export function hunksToUnifiedDiff(hunks: FileSystemEditHunk[]): string {
  return hunks.map(h => {
    const header = `@@ -${h.Old_start},${h.Old_lines} +${h.New_start},${h.New_lines} @@`
    return [header, ...h.Lines].join('\n')
  }).join('\n')
}

export function iconFromPath(path: string | undefined): FileChangeEntry['icon'] {
  if (!path) return 'other'
  const ext = path.split('.').pop()?.toLowerCase()
  switch (ext) {
    case 'tsx': return 'tsx'
    case 'ts': return 'ts'
    case 'css': return 'css'
    case 'json': return 'json'
    case 'md': return 'md'
    default: return 'other'
  }
}

/** Infer a concrete tool name from the JSON input when the declared name
 *  is generic (e.g. 'tool_call'). This ensures edit/search/bash etc. frames
 *  keep their specialised UI even when loaded from raw session storage. */
export function inferToolNameFromInput(input: string | undefined, fallback: string): string {
  // Trust known callable IDs from fallback before input-based inference.
  // Normalize LLM-facing aliases (project.read / project_read / project-read /
  // read) and legacy persisted IDs (project.file_read / project_file_read /
  // project-file_read / file_read) down to the bare tool name, then map to
  // the canonical project.* ID. The hyphen form comes from the historical
  // tool-registry name folding (dot -> hyphen) for provider-facing names.
  const fb = fallback.toLowerCase()
  let bare = fb
  if (bare.startsWith('project.')) bare = bare.slice('project.'.length)
  else if (bare.startsWith('project_')) bare = bare.slice('project_'.length)
  else if (bare.startsWith('project-')) bare = bare.slice('project-'.length)
  if (bare.startsWith('file_')) bare = bare.slice('file_'.length)
  switch (bare) {
    case 'list':
    case 'list_json': return 'project.list'
    case 'glob': return 'project.glob'
    case 'grep': return 'project.grep'
    case 'read_base64': return 'project.read_base64'
    case 'read_chunk': return 'project.read_chunk'
    case 'read': return 'project.read'
    case 'edit': return 'project.edit'
    case 'write': return 'project.write'
    case 'rm': return 'project.rm'
    case 'shell_exec': return 'project.shell_exec'
    // SSH tool names — must be matched before Command-based inference to
    // avoid misidentifying shell_run (which carries a Command field) as
    // project.shell_exec. The hyphen form comes from tool-registry dot →
    // hyphen folding; the bare forms appear when the LLM uses short names.
    case 'sshmanager.shell_run':
    case 'sshmanager-shell_run':
    case 'shell_run':
    case 'ssh_run': return 'sshmanager.shell_run'
    case 'sshmanager.shell_open':
    case 'sshmanager-shell_open':
    case 'shell_open':
    case 'ssh_open': return 'sshmanager.shell_open'
  }
  // computeruse.screenshot reaches the step as its LLM-facing name: the bare
  // name 'screenshot' or the hyphenated 'computeruse-screenshot' / underscored
  // 'computeruse_screenshot' (from dot-to-hyphen folding in toolregistry).
  if (fb === 'screenshot' || fb === 'computeruse_screenshot' || fb === 'computeruse-screenshot') return 'computeruse.screenshot'

  if (!input) return fallback
  try {
    const parsed: unknown = JSON.parse(input)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return fallback
    const rec = parsed as Record<string, unknown>
    if (rec.Old_string != null || rec.old_string != null) return 'project.edit'
    if (rec.Content != null || rec.content != null) return 'project.write'
    if (rec.command != null || rec.Command != null) return 'project.shell_exec'
    if (rec.pattern != null || rec.Pattern != null || rec.query != null || rec.Query != null) return 'project.grep'
    if (rec.path != null || rec.Path != null) {
      if (rec.recursive != null || rec.Recursive != null ||
          rec.exclude != null || rec.Exclude != null ||
          rec.depth != null || rec.Depth != null ||
          rec.detail != null || rec.Detail != null) return 'project.list'
      return 'project.read'
    }
    if (rec.name != null && typeof rec.name === 'string') return rec.name
  } catch { /* ignore */ }
  return fallback
}
