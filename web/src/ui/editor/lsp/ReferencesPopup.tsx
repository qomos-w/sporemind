import React, { useEffect, useMemo, useRef, useState } from 'react'
import { Input, TabsRoot, TabsList, TabsTrigger } from '../../settings/shadcn/ui'
import type { Location } from './lspClient'
import { uriToPath } from './lspClient'
import { highlightGoLine } from './highlightGo'
import type { RefsPopupAnchor, RefsPopupQueryPos } from './lspGotoDefinition'

export type RefsScope = 'project' | 'workspace'

export interface LineRange {
  Content: string
  StartLine: number
}

export interface ReferencesPopupProps {
  locations: Location[]
  anchor: RefsPopupAnchor
  queryPos: RefsPopupQueryPos
  projectRootPath?: string
  /** Read a line range from a file for the code-line preview. */
  fetchLines?: (path: string, offset: number, limit: number) => Promise<LineRange>
  /** Resolve references across all open workspace projects. */
  onFetchWorkspaceRefs?: (queryPos: RefsPopupQueryPos) => Promise<Location[]>
  onClose: () => void
  onJump: (filePath: string, line: number) => void
}

interface RefEntry {
  path: string
  displayPath: string
  line: number
}

const MAX_PATH_LEN = 56
const MAX_CODE_LEN = 160

function relativizePath(path: string, projectRootPath?: string): string {
  const normalized = path.replace(/\\/g, '/')
  if (!projectRootPath) return normalized
  const root = projectRootPath.replace(/\\/g, '/').replace(/\/+$/, '')
  if (root && normalized.toLowerCase().startsWith(`${root.toLowerCase()}/`)) {
    return normalized.slice(root.length + 1)
  }
  return normalized
}

function truncatePath(path: string, maxLen = MAX_PATH_LEN): string {
  if (path.length <= maxLen) return path
  const keep = Math.floor((maxLen - 1) / 2)
  return `${path.slice(0, keep)}…${path.slice(path.length - keep)}`
}

function truncateCode(code: string, maxLen = MAX_CODE_LEN): string {
  const trimmed = code.trim()
  if (trimmed.length <= maxLen) return trimmed
  return `${trimmed.slice(0, maxLen - 1)}…`
}

const CodeLine = React.memo(function CodeLine({ full, display }: { full: string; display: string }) {
  const spans = highlightGoLine(display)
  return (
    <span className="font-mono text-xs" title={full}>
      {spans.map((s, i) => (
        <span key={i} style={s.color ? { color: s.color } : undefined}>{s.text}</span>
      ))}
    </span>
  )
})

/**
 * Floating references search box (shadcn primitives, theme-adaptive via app
 * tokens). Lists Location[] entries as path:line plus a syntax-highlighted
 * code-line preview, with a project/workspace scope switch, filter input,
 * and keyboard navigation.
 */
export const ReferencesPopup: React.FC<ReferencesPopupProps> = ({
  locations,
  anchor,
  queryPos,
  projectRootPath,
  fetchLines,
  onFetchWorkspaceRefs,
  onClose,
  onJump,
}) => {
  const [filter, setFilter] = useState('')
  const [activeIndex, setActiveIndex] = useState(0)
  const [scope, setScope] = useState<RefsScope>('project')
  const [workspaceLocations, setWorkspaceLocations] = useState<Location[] | null>(null)
  const [workspaceError, setWorkspaceError] = useState<string | null>(null)
  const [lineText, setLineText] = useState<Record<string, string>>({})
  const inputRef = useRef<HTMLInputElement>(null)
  const popupRef = useRef<HTMLDivElement>(null)
  const mountedRef = useRef(true)
  const fetchLinesRef = useRef(fetchLines)
  fetchLinesRef.current = fetchLines

  useEffect(() => () => { mountedRef.current = false }, [])

  const activeLocations = scope === 'project' ? locations : (workspaceLocations ?? [])

  const entries: RefEntry[] = useMemo(() => {
    return activeLocations.map((loc) => {
      const path = uriToPath(loc.uri)
      return {
        path,
        displayPath: truncatePath(relativizePath(path, projectRootPath)),
        line: loc.range.start.line + 1,
      }
    })
  }, [activeLocations, projectRootPath])

  const filtered = useMemo(() => {
    const q = filter.trim().toLowerCase()
    if (!q) return entries
    return entries.filter((e) => e.path.toLowerCase().includes(q) || String(e.line).includes(q))
  }, [entries, filter])

  useEffect(() => {
    setActiveIndex((prev) => Math.min(prev, Math.max(filtered.length - 1, 0)))
  }, [filtered])

  useEffect(() => {
    inputRef.current?.focus()
  }, [])

  useEffect(() => {
    const handle = (e: MouseEvent) => {
      if (popupRef.current && !popupRef.current.contains(e.target as Node)) {
        onClose()
      }
    }
    document.addEventListener('mousedown', handle)
    return () => document.removeEventListener('mousedown', handle)
  }, [onClose])

  // Fetch the referenced source line for each entry: one grouped read per
  // file covering all its entry lines. filesystem.read is 1-based (Offset =
  // first line number, StartLine echoes it back), so convert the 0-based LSP
  // line numbers to 1-based when requesting and when indexing the response.
  useEffect(() => {
    const fn = fetchLinesRef.current
    if (!fn) return
    const byPath = new Map<string, number[]>()
    for (const e of entries) {
      const zeroBased = e.line - 1
      const lines = byPath.get(e.path) ?? []
      lines.push(zeroBased)
      byPath.set(e.path, lines)
    }
    const pending: Promise<void>[] = []
    for (const [path, lines] of byPath) {
      const min = Math.min(...lines)
      const max = Math.max(...lines)
      const offset = min + 1 // 1-based first line
      pending.push(
        fn(path, offset, max - min + 1)
          .then((resp) => {
            if (!mountedRef.current) return
            setLineText((prev) => {
              const next = { ...prev }
              const rows = resp.Content.split('\n')
              const firstLine = resp.StartLine // 1-based
              for (const l of lines) {
                next[`${path}:${l}`] = rows[l - (firstLine - 1)] ?? ''
              }
              return next
            })
          })
          .catch(() => {
            if (!mountedRef.current) return
            setLineText((prev) => {
              const next = { ...prev }
              for (const l of lines) next[`${path}:${l}`] = ''
              return next
            })
          }),
      )
    }
  }, [entries])

  const switchScope = (value: string): void => {
    if (value === 'project') {
      setScope('project')
      return
    }
    if (value !== 'workspace') return
    setScope('workspace')
    if (workspaceLocations === null && !workspaceError && onFetchWorkspaceRefs) {
      onFetchWorkspaceRefs(queryPos)
        .then((locs) => {
          if (mountedRef.current) setWorkspaceLocations(locs)
        })
        .catch((err: unknown) => {
          if (mountedRef.current) setWorkspaceError(err instanceof Error ? err.message : String(err))
        })
    }
  }

  const jump = (index: number): void => {
    const entry = filtered[index]
    if (!entry) return
    onJump(entry.path, entry.line - 1)
    onClose()
  }

  const handleKeyDown = (e: React.KeyboardEvent): void => {
    if (filtered.length === 0) {
      if (e.key === 'Escape') {
        e.preventDefault()
        onClose()
      }
      return
    }
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setActiveIndex((i) => (i + 1) % filtered.length)
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setActiveIndex((i) => (i - 1 + filtered.length) % filtered.length)
    } else if (e.key === 'Enter') {
      e.preventDefault()
      jump(activeIndex)
    } else if (e.key === 'Escape') {
      e.preventDefault()
      onClose()
    }
  }

  return (
    <div
      ref={popupRef}
      className="shadcn-scope fixed z-[1000] flex w-[640px] max-w-[85vw] flex-col rounded-lg border border-border bg-popover text-popover-foreground shadow-lg"
      style={{ left: Math.min(anchor.x, window.innerWidth - 660), top: anchor.y }}
      onKeyDown={handleKeyDown}
    >
      <div className="flex items-center gap-2 p-2">
        <Input
          ref={inputRef}
          className="h-8 flex-1 font-mono text-xs"
          placeholder="Filter references…"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
        {onFetchWorkspaceRefs && (
          <TabsRoot value={scope} onValueChange={switchScope} className="w-auto">
            <TabsList className="h-8">
              <TabsTrigger value="project" className="px-2 text-xs">Project</TabsTrigger>
              <TabsTrigger value="workspace" className="px-2 text-xs">Workspace</TabsTrigger>
            </TabsList>
          </TabsRoot>
        )}
      </div>
      <div className="max-h-[320px] overflow-y-auto">
        {scope === 'workspace' && workspaceLocations === null && !workspaceError && (
          <div className="px-3 py-2 text-xs text-muted-foreground">Searching workspace…</div>
        )}
        {scope === 'workspace' && workspaceError && (
          <div className="px-3 py-2 text-xs text-muted-foreground">Workspace search failed: {workspaceError}</div>
        )}
        {filtered.length === 0 && !(scope === 'workspace' && workspaceLocations === null) ? (
          <div className="px-3 py-2 text-xs text-muted-foreground">No matches</div>
        ) : (
          <ul>
            {filtered.map((entry, i) => {
              const code = lineText[`${entry.path}:${entry.line - 1}`]
              return (
                <li
                  key={`${entry.path}:${entry.line}:${i}`}
                  className={`flex cursor-pointer items-baseline gap-2 overflow-hidden px-3 py-1.5 text-xs${i === activeIndex ? ' bg-accent' : ''}`}
                  onMouseEnter={() => setActiveIndex(i)}
                  onClick={() => jump(i)}
                  title={`${entry.path}:${entry.line}`}
                >
                  <span className="shrink-0 font-mono text-muted-foreground">{entry.displayPath}</span>
                  <span className="shrink-0 font-mono text-muted-foreground/70">:{entry.line}</span>
                  <span className="min-w-0 flex-1 truncate text-left">
                    {code === undefined ? (
                      <span className="font-mono text-xs text-muted-foreground/60">…</span>
                    ) : (
                      <CodeLine full={code} display={truncateCode(code)} />
                    )}
                  </span>
                </li>
              )
            })}
          </ul>
        )}
      </div>
    </div>
  )
}
