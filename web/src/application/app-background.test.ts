import { describe, expect, it } from 'vitest'
import {
  activeBackgroundImage,
  addBackgroundImage,
  applyAppBackground,
  parseAppBackground,
  removeBackgroundImage,
  selectBackgroundImage,
  type AppBackground,
} from './app-background'

const A = 'data:image/webp;base64,AAA'
const B = 'data:image/png;base64,BBB'
const C = 'data:image/jpeg;base64,CCC'

describe('parseAppBackground', () => {
  it('returns null for missing or empty values', () => {
    expect(parseAppBackground(undefined)).toBeNull()
    expect(parseAppBackground('')).toBeNull()
  })

  it('returns null for corrupted JSON', () => {
    expect(parseAppBackground('{oops')).toBeNull()
    expect(parseAppBackground('"plain"')).toBeNull()
    expect(parseAppBackground('{"image":"http://evil"}')).toBeNull()
    expect(parseAppBackground('{"images":[]}')).toBeNull()
  })

  it('parses a list entry and clamps active into range', () => {
    expect(parseAppBackground(JSON.stringify({ images: [A, B], active: 1 }))).toEqual({ images: [A, B], active: 1 })
    expect(parseAppBackground(JSON.stringify({ images: [A, B], active: 9 }))).toEqual({ images: [A, B], active: 1 })
    expect(parseAppBackground(JSON.stringify({ images: [A, B] }))).toEqual({ images: [A, B], active: 0 })
  })

  it('keeps active -1 (default) and clamps below -1', () => {
    expect(parseAppBackground(JSON.stringify({ images: [A, B], active: -1 }))).toEqual({ images: [A, B], active: -1 })
    expect(parseAppBackground(JSON.stringify({ images: [A, B], active: -5 }))).toEqual({ images: [A, B], active: -1 })
  })

  it('filters non-image entries out of the list', () => {
    expect(parseAppBackground(JSON.stringify({ images: [A, 'http://x', 42] }))).toEqual({ images: [A], active: 0 })
  })

  it('migrates a legacy single-image entry', () => {
    expect(parseAppBackground(JSON.stringify({ image: B }))).toEqual({ images: [B], active: 0 })
  })
})

describe('list helpers', () => {
  it('addBackgroundImage appends and activates the new image', () => {
    expect(addBackgroundImage(null, A)).toEqual({ images: [A], active: 0 })
    expect(addBackgroundImage({ images: [A], active: 0 }, B)).toEqual({ images: [A, B], active: 1 })
  })

  it('selectBackgroundImage switches active within range', () => {
    const bg: AppBackground = { images: [A, B, C], active: 0 }
    expect(selectBackgroundImage(bg, 2)).toEqual({ images: [A, B, C], active: 2 })
    expect(selectBackgroundImage(bg, 9)).toBe(bg)
  })

  it('selectBackgroundImage accepts -1 as the default (no image) option', () => {
    const bg: AppBackground = { images: [A, B, C], active: 2 }
    expect(selectBackgroundImage(bg, -1)).toEqual({ images: [A, B, C], active: -1 })
    expect(selectBackgroundImage({ images: [A], active: -1 }, -1).active).toBe(-1)
  })

  it('removeBackgroundImage keeps active on the same image when possible', () => {
    const bg: AppBackground = { images: [A, B, C], active: 2 }
    expect(removeBackgroundImage(bg, 0)).toEqual({ images: [B, C], active: 1 })
    expect(removeBackgroundImage(bg, 2)).toEqual({ images: [A, B], active: 1 })
  })

  it('removeBackgroundImage returns null when the last image is removed', () => {
    expect(removeBackgroundImage({ images: [A], active: 0 }, 0)).toBeNull()
  })

  it('activeBackgroundImage resolves the active entry', () => {
    expect(activeBackgroundImage(null)).toBeNull()
    expect(activeBackgroundImage({ images: [A, B], active: 1 })).toBe(B)
    expect(activeBackgroundImage({ images: [A, B], active: -1 })).toBeNull()
  })
})

describe('applyAppBackground', () => {
  it('sets body class and CSS variable to the active image', () => {
    applyAppBackground({ images: [A, B], active: 1 })
    expect(document.body.classList.contains('app-bg-active')).toBe(true)
    expect(document.body.style.getPropertyValue('--app-bg-image')).toBe(`url("${B}")`)
  })

  it('removes class and CSS variable when cleared', () => {
    applyAppBackground({ images: [A], active: 0 })
    applyAppBackground(null)
    expect(document.body.classList.contains('app-bg-active')).toBe(false)
    expect(document.body.style.getPropertyValue('--app-bg-image')).toBe('')
  })

  it('removes class and CSS variable when default (-1) is selected', () => {
    applyAppBackground({ images: [A], active: 0 })
    applyAppBackground({ images: [A], active: -1 })
    expect(document.body.classList.contains('app-bg-active')).toBe(false)
    expect(document.body.style.getPropertyValue('--app-bg-image')).toBe('')
  })
})
