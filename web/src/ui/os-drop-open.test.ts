import { describe, it, expect } from 'vitest'
import {
  OS_DROP_OPEN_MAX_BYTES,
  osDropOpenKind,
  bytesLookBinary,
  imageDataUrl,
  dropIsUnclaimed,
} from './os-drop-open'

describe('osDropOpenKind', () => {
  it('routes image extensions to the image viewer', () => {
    expect(osDropOpenKind('photo.PNG', 10)).toBe('image')
    expect(osDropOpenKind('icon.svg', 10)).toBe('image')
  })

  it('routes everything else to the text viewer', () => {
    expect(osDropOpenKind('notes.txt', 10)).toBe('text')
    expect(osDropOpenKind('Makefile', 10)).toBe('text')
  })

  it('skips oversized entries', () => {
    expect(osDropOpenKind('a.txt', OS_DROP_OPEN_MAX_BYTES + 1)).toBe('skip')
    expect(osDropOpenKind('a.png', OS_DROP_OPEN_MAX_BYTES + 1)).toBe('skip')
  })
})

describe('bytesLookBinary', () => {
  it('accepts plain text', () => {
    expect(bytesLookBinary(new TextEncoder().encode('hello world'))).toBe(false)
  })

  it('rejects a NUL byte in the head', () => {
    const bytes = new Uint8Array(16)
    bytes[3] = 0
    expect(bytesLookBinary(bytes)).toBe(true)
  })

  it('ignores NUL bytes beyond the probe window', () => {
    const bytes = new Uint8Array(9000).fill(0xff)
    bytes[8192] = 0
    expect(bytesLookBinary(bytes)).toBe(false)
  })
})

describe('imageDataUrl', () => {
  it('uses the extension mime and a base64 body', () => {
    expect(imageDataUrl('x.png', new Uint8Array([1, 2]))).toBe('data:image/png;base64,AQI=')
  })
})

describe('dropIsUnclaimed', () => {
  it('requires an element target', () => {
    expect(dropIsUnclaimed(null, document.createElement('div'))).toBe(false)
  })

  it('treats drops inside a claimed target as claimed', () => {
    const root = document.createElement('div')
    const claimed = document.createElement('div')
    claimed.setAttribute('data-file-drop-target', '')
    const leaf = document.createElement('span')
    claimed.appendChild(leaf)
    root.appendChild(claimed)
    expect(dropIsUnclaimed(leaf, root)).toBe(false)
  })

  it('treats direct children of the root as unclaimed', () => {
    const root = document.createElement('div')
    const leaf = document.createElement('span')
    root.appendChild(leaf)
    expect(dropIsUnclaimed(leaf, root)).toBe(true)
  })
})
