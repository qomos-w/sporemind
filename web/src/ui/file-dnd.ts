/**
 * Cross-panel file drag-and-drop.
 *
 * The local FileBrowser and the remote SSH FTP panel live in different React
 * subtrees but can both be visible at the same time (files mode + an SSH tab in
 * the right panel). To transfer files between them we piggyback on the native
 * HTML5 drag session via custom MIME types that carry the source origin and
 * identifying info.
 *
 * Two MIME types are used — one per origin — because {@link DataTransfer.getData}
 * is unavailable during `dragover` (the spec hides payload values until `drop`).
 * Inspecting {@link DataTransfer.types} is enough to tell whether a drag carries
 * a *foreign* payload, which lets each panel accept/reject and set the correct
 * drop effect during dragover without reading the payload.
 */

/** Custom MIME type for drags that originate from the local FileBrowser. */
export const LOCAL_FILE_DND_MIME = 'application/x-sporemind-file-local'

/** Custom MIME type for drags that originate from the remote SSH FTP panel. */
export const REMOTE_FILE_DND_MIME = 'application/x-sporemind-file-remote'

export type FileDragOrigin = 'local' | 'remote'

/**
 * Payload serialized into the custom DataTransfer slot. Carries enough
 * information for the receiving panel to read the source file.
 */
export interface FileDragPayload {
  /** Which panel the drag originated from. */
  origin: FileDragOrigin
  /** Display name of the dragged file/folder. */
  name: string
  /** Path of the dragged entry (relative for local, absolute for remote). */
  path: string
  /** Whether the entry is a directory. */
  isDir: boolean
  /** Local origin: project id (to read the file via the project actor). */
  projectId?: string
  /** Remote origin: SSH session id (to read the file via the sshmanager actor). */
  sessionId?: string
}

const MIME_BY_ORIGIN: Record<FileDragOrigin, string> = {
  local: LOCAL_FILE_DND_MIME,
  remote: REMOTE_FILE_DND_MIME,
}

/** MIME type for payloads that originate from the *other* panel. */
function foreignMime(panelOrigin: FileDragOrigin): string {
  return panelOrigin === 'local' ? REMOTE_FILE_DND_MIME : LOCAL_FILE_DND_MIME
}

/** Prefix marking our text/plain payload (distinguishes from plain-text drags). */
const DND_MARKER = 'x-sporemind-dnd:'

/** Attach the cross-panel payload to an outgoing drag. */
export function setFileDragPayload(dt: DataTransfer, payload: FileDragPayload): void {
  dt.setData(MIME_BY_ORIGIN[payload.origin], JSON.stringify(payload))
  // Also set text/plain with a marker prefix. In some WebView2 versions the
  // custom MIME type is not visible in DataTransfer.types during dragover
  // (protected mode), which prevents the drop target from calling
  // preventDefault and blocks the drop entirely. text/plain is always visible.
  // The marker prefix distinguishes our payload from plain-text editor drags.
  dt.setData('text/plain', DND_MARKER + JSON.stringify(payload))
}

/**
 * Whether a drag session carries a payload from the *other* panel.
 * Safe to call during `dragover` (inspects {@link DataTransfer.types} only).
 */
export function hasForeignFileDrag(dt: DataTransfer, panelOrigin: FileDragOrigin): boolean {
  return dt.types.includes(foreignMime(panelOrigin))
}

/**
 * Read the foreign payload during a `drop` event.
 * Returns null when no foreign payload is present (or on parse error).
 */
export function getForeignFileDragPayload(
  dt: DataTransfer,
  panelOrigin: FileDragOrigin,
): FileDragPayload | null {
  const mime = foreignMime(panelOrigin)
  let raw = dt.getData(mime)
  if (!raw) {
    // Fallback: text/plain carries the same JSON payload (with marker prefix).
    const tp = dt.getData('text/plain')
    if (tp.startsWith(DND_MARKER)) raw = tp.slice(DND_MARKER.length)
  }
  if (!raw) return null
  try {
    const payload = JSON.parse(raw) as FileDragPayload
    // Verify the payload is from the foreign panel (not our own).
    if (payload.origin !== (panelOrigin === 'local' ? 'remote' : 'local')) return null
    return payload
  } catch {
    return null
  }
}

/**
 * Read a local-origin payload during a `drop` event, regardless of which panel
 * reads it. Used by drop targets outside the file panels (e.g. the plugin
 * iframe overlay) that want to act on a FileBrowser drag.
 */
export function getLocalFileDragPayload(dt: DataTransfer): FileDragPayload | null {
  let raw = dt.getData(LOCAL_FILE_DND_MIME)
  if (!raw) {
    const tp = dt.getData('text/plain')
    if (tp.startsWith(DND_MARKER)) raw = tp.slice(DND_MARKER.length)
  }
  if (!raw) return null
  try {
    const payload = JSON.parse(raw) as FileDragPayload
    return payload.origin === 'local' ? payload : null
  } catch {
    return null
  }
}

/* -------------------------------------------------------------------------- */
/* Drag session tracking                                                      */
/* -------------------------------------------------------------------------- */

/**
 * Dragover/drop events over an `<iframe>` (plugin panels) never reach the host
 * document, so drop targets cannot discover a FileBrowser drag by hovering.
 * Drag sources bracket the native drag session with begin/end calls, letting
 * iframe hosts mount a transparent overlay that captures the drop instead.
 */
let localDragActive = false
const localDragListeners = new Set<() => void>()

function notifyLocalDragListeners(): void {
  for (const listener of localDragListeners) listener()
}

export function beginLocalFileDragSession(): void {
  if (localDragActive) return
  localDragActive = true
  notifyLocalDragListeners()
}

export function endLocalFileDragSession(): void {
  if (!localDragActive) return
  localDragActive = false
  notifyLocalDragListeners()
}

/** useSyncExternalStore subscription for the local drag session flag. */
export function subscribeLocalFileDrag(listener: () => void): () => void {
  localDragListeners.add(listener)
  return () => { localDragListeners.delete(listener) }
}

/** useSyncExternalStore snapshot for the local drag session flag. */
export function getLocalFileDragActive(): boolean {
  return localDragActive
}
