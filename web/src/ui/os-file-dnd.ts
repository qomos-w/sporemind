/**
 * Unified OS-level file drag-and-drop interface (desktop + web).
 *
 * Two directions, two primitives:
 *
 *  1. OS → app (`attachOsFileDrop`): a native HTML5 `drop` on an element is
 *     expanded (via `webkitGetAsEntry`) into a flat list of `OsDroppedEntry`
 *     (files and directories, `'/'`-separated relPaths). `dragover` is
 *     `preventDefault()`-ed so the browser/WebView does not navigate to the
 *     dropped file. File bytes are exposed lazily through `readBytes()` so the
 *     caller decides what to read and where to write it (project write_base64 /
 *     sshmanager file_write_base64 are wired by the FileBrowser/SshSessionView
 *     integration tasks — this module stays interface + pure logic).
 *
 *  2. app → OS (`startOsFileDragOut`): handed a list of local (project) or
 *     remote (SSH) items, resolves local items to absolute paths via
 *     `project.info` (root = `Roots[0]`, mirroring the Go actor's
 *     `filepath.Join(a.Roots[0].Path, rel)`), exports remote items to a
 *     tar.gz base64 via `sshmanager.archive_export`, then invokes the desktop
 *     binding `StartFileDragOut` which performs the native OLE drag.
 *
 * Desktop binding convention: the Go side (pkg/desktop, see task card
 * 「桌面原生拖出 StartFileDragOut」) exposes
 * `StartFileDragOut(FileDragOutRequest{localPaths, archiveTarGzBase64, archiveRootName})`.
 * The generated binding may not exist yet when this module ships, so the call
 * goes through `Call.ByName` with the agreed method name, guarded by
 * `isWails` — non-desktop callers get `{ok: false, reason: 'not-desktop'}`
 * and never touch `@wailsio/runtime`. The desktop contract carries a single
 * archive, so at most one remote item per drag is supported.
 */

import { isWails } from '../application/runtime'
import { client } from '../application/generated-client'
import * as projectClient from '../gen-clients/project/client'
import * as sshmanagerClient from '../gen-clients/sshmanager/client'

/**
 * Under Wails (EnableFileDrop), the runtime installs a document-level
 * `dragover` listener that forces `dropEffect = "none"` — the prohibited
 * cursor — unless the element under the cursor sits inside an element
 * carrying this attribute. Every OS-drop zone must therefore carry it,
 * otherwise external file drags show the no-drop cursor even though our own
 * handlers accept the drop.
 */
export const OS_DROP_TARGET_ATTR = 'data-file-drop-target'

/* -------------------------------------------------------------------------- */
/* OS → app: dropped entry tree                                               */
/* -------------------------------------------------------------------------- */

/** One entry from an OS drop, flattened out of the dropped directory tree. */
export interface OsDroppedEntry {
  /** Display name (last path segment). */
  name: string
  /** Path relative to the drop root, `'/'`-separated; top level is just `name`. */
  relPath: string
  /** Whether this entry is a directory (directories are emitted before their children). */
  isDir: boolean
  /** Byte size for files; 0 for directories. */
  size: number
  /** Read the file bytes. Rejects for directories. */
  readBytes(): Promise<Uint8Array>
}

/** Options for {@link attachOsFileDrop}. */
export interface OsFileDropOptions {
  /** Receives the flattened drop tree (dirs first, depth-first). */
  onDrop(entries: OsDroppedEntry[]): void | Promise<void>
  /** Expansion/reading errors land here instead of disappearing. */
  onError?(err: unknown): void
}

/** Wrap the callback-style `FileSystemFileEntry.file()` in a promise. */
function entryFile(entry: FileSystemFileEntry): Promise<File> {
  return new Promise((resolve, reject) => entry.file(resolve, reject))
}

/**
 * Read all children of a directory reader. `readEntries` may deliver at most
 * 100 entries per call and only an empty batch signals the end, so this loops
 * until an empty batch arrives.
 */
function readDirEntries(reader: FileSystemDirectoryReader): Promise<FileSystemEntry[]> {
  return new Promise((resolve, reject) => {
    const all: FileSystemEntry[] = []
    const step = (): void => {
      reader.readEntries(
        (batch) => {
          if (batch.length === 0) {
            resolve(all)
            return
          }
          all.push(...batch)
          step()
        },
        reject,
      )
    }
    step()
  })
}

function fileEntryFromFile(file: File, relPath: string): OsDroppedEntry {
  return {
    name: file.name,
    relPath,
    isDir: false,
    size: file.size,
    readBytes: () => file.arrayBuffer().then((buf) => new Uint8Array(buf)),
  }
}

/**
 * Recursively flatten one file-system entry into `out`.
 * Errors (unreadable file / undecodable directory) are collected instead of
 * aborting the whole tree — a single unreadable subtree should not lose the
 * rest of the drop.
 */
async function expandEntry(
  entry: FileSystemEntry,
  prefix: string,
  out: OsDroppedEntry[],
  errors: unknown[],
): Promise<void> {
  if (!entry.name) return // unnamed entries cannot be addressed on disk
  const relPath = prefix ? `${prefix}/${entry.name}` : entry.name
  if (entry.isDirectory) {
    out.push({
      name: entry.name,
      relPath,
      isDir: true,
      size: 0,
      readBytes: () =>
        Promise.reject(new Error(`os-file-dnd: '${relPath}' is a directory`)),
    })
    try {
      const children = await readDirEntries((entry as FileSystemDirectoryEntry).createReader())
      for (const child of children) await expandEntry(child, relPath, out, errors)
    } catch (err) {
      errors.push(err)
    }
    return
  }
  if (entry.isFile) {
    try {
      const file = await entryFile(entry as FileSystemFileEntry)
      out.push(fileEntryFromFile(file, relPath))
    } catch (err) {
      errors.push(err)
    }
  }
  // Other entry kinds (symlinks on some platforms report isFile/isDirectory
  // false) are skipped — bytes would not be addressable anyway.
}

/**
 * Capture entries synchronously. `DataTransferItemList` and the entries it
 * hands out are only valid during the drop event dispatch, so every entry/file
 * reference must be grabbed before the first `await`.
 */
function captureDropSources(dt: DataTransfer | null): {
  entries: FileSystemEntry[]
  files: File[]
} {
  const entries: FileSystemEntry[] = []
  const files: File[] = []
  const items = dt?.items
  if (!items) return { entries, files }
  for (let i = 0; i < items.length; i++) {
    const item = items[i]
    if (!item || item.kind !== 'file') continue
    const entry =
      typeof item.webkitGetAsEntry === 'function' ? item.webkitGetAsEntry() : null
    if (entry) {
      entries.push(entry)
      continue
    }
    // No entry API (older WebView) — a bare File is still usable at top level.
    const file = typeof item.getAsFile === 'function' ? item.getAsFile() : null
    if (file) files.push(file)
  }
  return { entries, files }
}

/**
 * Expand a drop's `DataTransfer` into the flattened `OsDroppedEntry` tree.
 * Throws when any subtree failed to expand (callers surface via `onError`).
 */
export async function collectOsDroppedEntries(dt: DataTransfer | null): Promise<OsDroppedEntry[]> {
  const { entries, files } = captureDropSources(dt)
  const out: OsDroppedEntry[] = []
  const errors: unknown[] = []
  for (const entry of entries) await expandEntry(entry, '', out, errors)
  for (const file of files) out.push(fileEntryFromFile(file, file.name))
  if (errors.length > 0) {
    const first = errors[0]
    throw new Error(
      `os-file-dnd: failed to expand ${errors.length} dropped entr${errors.length === 1 ? 'y' : 'ies'}: ${
        first instanceof Error ? first.message : String(first)
      }`,
    )
  }
  return out
}

/**
 * Attach OS-file drop handling to an element.
 *
 * `dragover` is unconditionally `preventDefault()`-ed (without it the browser
 * or WebView2 would navigate to the dropped file instead of firing `drop`).
 * Returns a detach function that removes both listeners.
 */
export function attachOsFileDrop(el: HTMLElement, opts: OsFileDropOptions): () => void {
  const onDragOver = (e: DragEvent): void => {
    e.preventDefault()
  }
  const onDrop = (e: DragEvent): void => {
    e.preventDefault()
    void collectOsDroppedEntries(e.dataTransfer)
      .then((entries) => opts.onDrop(entries))
      .catch((err: unknown) => opts.onError?.(err))
  }
  el.addEventListener('dragover', onDragOver)
  el.addEventListener('drop', onDrop)
  return () => {
    el.removeEventListener('dragover', onDragOver)
    el.removeEventListener('drop', onDrop)
  }
}

/* -------------------------------------------------------------------------- */
/* app → OS: native drag-out (desktop only)                                   */
/* -------------------------------------------------------------------------- */

/** A local (project) file/folder, identified by project + root-relative path. */
export interface OsDragOutLocalItem {
  kind: 'local'
  projectId: string
  relPath: string
}

/** A remote (SSH) file/folder, identified by session + absolute remote path. */
export interface OsDragOutRemoteItem {
  kind: 'remote'
  sessionId: string
  remotePath: string
}

export type OsDragOutItem = OsDragOutLocalItem | OsDragOutRemoteItem

/**
 * Wire shape of the desktop `StartFileDragOut` binding — mirrors
 * `pkg/desktop.FileDragOutRequest` and its json tags exactly:
 * `LocalPaths` drags existing files directly; `ArchiveTarGzBase64` +
 * `ArchiveRootName` extract to a temp dir first (Go side defaults the root
 * name to "sporemind-dragout" when empty).
 */
export interface OsDragOutRequest {
  localPaths: string[]
  archiveTarGzBase64: string
  archiveRootName: string
}

export type OsDragOutFailureReason = 'not-desktop' | 'unsupported' | 'error'

export interface OsDragOutResult {
  ok: boolean
  /** Set when `ok` is false. */
  reason?: OsDragOutFailureReason
  /** Human-readable detail for `unsupported` / `error`. */
  error?: string
}

/** Injectable seams so tests (and the future wails-runtime wrapper) can substitute the desktop side. */
export interface OsDragOutDeps {
  /** Desktop availability; defaults to `isWails` from application/runtime. */
  isDesktop?: () => boolean
  /** Desktop binding invocation; defaults to the wails-runtime `StartFileDragOut` wrapper (generated ByID binding). */
  invokeDragOut?: (req: OsDragOutRequest) => Promise<unknown>
}

async function defaultInvokeDragOut(req: OsDragOutRequest): Promise<unknown> {
  // Dynamic import keeps the wails bindings (and their window._wails side
  // effects) out of every importer; the isWails guard in startOsFileDragOut
  // ensures this only runs inside the desktop webview. Must go through the
  // generated ByID binding — a hand-written Call.ByName FQN silently misses
  // (the registered FQN is `.../pkg/desktop.App.StartFileDragOut`, FNV-hashed).
  const { StartFileDragOut } = await import('../application/wails-runtime')
  const ok = await StartFileDragOut(req)
  if (!ok) throw new Error('desktop StartFileDragOut binding failed or unavailable')
  return ok
}

/**
 * Start a native drag-out of the given items to the OS (desktop only).
 *
 * Local items are resolved to absolute paths via `project.info` (first root);
 * a single remote item is exported to tar.gz base64 via
 * `sshmanager.archive_export`. Returns `{ok: false, reason: 'not-desktop'}`
 * outside the desktop webview — callers fall back to the in-app HTML5 drag.
 */
export async function startOsFileDragOut(
  items: OsDragOutItem[],
  deps: OsDragOutDeps = {},
): Promise<OsDragOutResult> {
  const isDesktop = deps.isDesktop ?? isWails
  if (!isDesktop()) return { ok: false, reason: 'not-desktop' }
  if (items.length === 0) {
    return { ok: false, reason: 'unsupported', error: 'no items to drag out' }
  }
  const localPaths: string[] = []
  let archiveBase64 = ''
  let archiveRootName = ''
  try {
    for (const item of items) {
      if (item.kind === 'local') {
        const info = await projectClient.info(client, { target: item.projectId })
        const root = info.Roots[0]
        if (!root) {
          return {
            ok: false,
            reason: 'error',
            error: `project.info: project '${item.projectId}' has no roots`,
          }
        }
        localPaths.push(joinOsPath(root.Path, item.relPath))
      } else {
        if (archiveBase64) {
          return {
            ok: false,
            reason: 'unsupported',
            error: 'drag-out supports at most one remote item per drag (single archive contract)',
          }
        }
        const exp = await sshmanagerClient.archiveExport(client, {
          SessionId: item.sessionId,
          Path: item.remotePath,
        })
        archiveBase64 = exp.Content
        archiveRootName = baseNameOf(item.remotePath)
      }
    }
    const invoke = deps.invokeDragOut ?? defaultInvokeDragOut
    await invoke({ localPaths: localPaths, archiveTarGzBase64: archiveBase64, archiveRootName: archiveRootName })
    return { ok: true }
  } catch (err) {
    return { ok: false, reason: 'error', error: err instanceof Error ? err.message : String(err) }
  }
}

/* -------------------------------------------------------------------------- */
/* Utilities                                                                  */
/* -------------------------------------------------------------------------- */

/** Chunked base64 encoding — avoids blowing the arg-count limit on large buffers. */
export function bytesToBase64(bytes: Uint8Array): string {
  let binary = ''
  for (let offset = 0; offset < bytes.length; offset += 0x8000) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + 0x8000))
  }
  return btoa(binary)
}

/**
 * Join a project root (absolute, OS-native separators) with a `'/'`-separated
 * root-relative path, mirroring the Go actor's `filepath.Join(Roots[0], rel)`.
 * The separator is inferred from the root so Windows backslash roots stay
 * backslash.
 */
export function joinOsPath(root: string, rel: string): string {
  const segments = rel.split('/').filter((s) => s && s !== '.')
  const trimmedRoot = root.replace(/[\\/]+$/, '')
  if (segments.length === 0) return trimmedRoot
  const sep = trimmedRoot.includes('\\') ? '\\' : '/'
  return trimmedRoot + sep + segments.join(sep)
}

/** Last `'/'`-separated segment of a path (used for the archive root name). */
export function baseNameOf(path: string): string {
  const parts = path.split('/').filter(Boolean)
  if (parts.length === 0) return path
  const last = parts[parts.length - 1]
  return last ?? path
}
