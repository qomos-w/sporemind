import { bytesToBase64 } from './os-file-dnd'
import { isImageExt, mimeFromExt } from './ai/components/parts/image-utils'

/** Entries larger than this are not opened — pulling a dropped multi-hundred-MB
 *  file into memory just to preview it would freeze the UI. */
export const OS_DROP_OPEN_MAX_BYTES = 8 * 1024 * 1024

export type OsDropOpenKind = 'image' | 'text' | 'skip'

export function osDropOpenKind(name: string, size: number): OsDropOpenKind {
  if (size > OS_DROP_OPEN_MAX_BYTES) return 'skip'
  if (isImageExt(name)) return 'image'
  return 'text'
}

/** NUL-byte sniff over the head of the file, mirroring the filesystem
 *  backend's binary rejection heuristic. */
export function bytesLookBinary(bytes: Uint8Array): boolean {
  return bytes.subarray(0, 8192).includes(0)
}

export function imageDataUrl(name: string, bytes: Uint8Array): string {
  return `data:${mimeFromExt(name)};base64,${bytesToBase64(bytes)}`
}

/** A drop opens in the right sidebar only when it did not land inside a claimed
 *  drop target (FileBrowser / SshSessionView mark themselves with
 *  data-file-drop-target). The shell root itself counts as unclaimed. */
export function dropIsUnclaimed(target: EventTarget | null, rootEl: EventTarget | null): boolean {
  if (!(target instanceof Element) || !(rootEl instanceof Element)) return false
  const claimed = target.closest('[data-file-drop-target]')
  return (claimed ?? rootEl) === rootEl
}
