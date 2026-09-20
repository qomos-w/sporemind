/**
 * Gate for opening a remote file in the in-app text editor from the FTP
 * browser. Pure so it can be unit-tested without an SSH session.
 */
export const EDIT_TEXT_MAX_BYTES = 1_048_576

export interface EditableFileLike {
  IsDir: boolean
  Size: number
}

export type EditGateResult =
  | { ok: true }
  | { ok: false; reason: 'directory' | 'tooLarge' }

export function shouldOpenForEdit(entry: EditableFileLike): EditGateResult {
  if (entry.IsDir) return { ok: false, reason: 'directory' }
  if (entry.Size > EDIT_TEXT_MAX_BYTES) return { ok: false, reason: 'tooLarge' }
  return { ok: true }
}
