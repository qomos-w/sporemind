import { describe, it, expect } from 'vitest'
import { EDIT_TEXT_MAX_BYTES, shouldOpenForEdit } from './sshFileEditGate'

describe('shouldOpenForEdit', () => {
  it('rejects directories', () => {
    expect(shouldOpenForEdit({ IsDir: true, Size: 0 })).toEqual({ ok: false, reason: 'directory' })
  })

  it('allows an empty file', () => {
    expect(shouldOpenForEdit({ IsDir: false, Size: 0 })).toEqual({ ok: true })
  })

  it('allows a file exactly at the threshold', () => {
    expect(shouldOpenForEdit({ IsDir: false, Size: EDIT_TEXT_MAX_BYTES })).toEqual({ ok: true })
  })

  it('rejects a file above the threshold', () => {
    expect(shouldOpenForEdit({ IsDir: false, Size: EDIT_TEXT_MAX_BYTES + 1 })).toEqual({ ok: false, reason: 'tooLarge' })
  })
})
