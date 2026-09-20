import { describe, it, expect, vi } from 'vitest'
import { resolveAppIcon } from './appIconResolver'

// Mock gateway so icon URL resolution is deterministic.
vi.mock('../../../application/gateway', () => ({
  getHttpUrl: () => 'http://127.0.0.1:18080',
}))

describe('resolveAppIcon', () => {
  it('renders a lucide icon by name', () => {
    const node = resolveAppIcon('shield-check', 'app.test', 14)
    expect(node).toBeDefined()
    // It should be a React element with a component (not <img>)
    expect((node as any).type).not.toBe('img')
  })

  it('injects the bundle color into the lucide glyph as an inline style and tags it', () => {
    const node = resolveAppIcon('shield-check', 'app.test', 16, '#2563eb') as any
    expect(node.type).not.toBe('img')
    expect(node.props.style).toEqual({ color: '#2563eb' })
    // Marker class the dark-theme contrast rule targets.
    expect(node.props.className).toBe('app-icon-colored')
  })

  it('omits the inline color style and marker class when no color is given', () => {
    const node = resolveAppIcon('shield-check', 'app.test', 16) as any
    expect(node.props.style).toBeUndefined()
    expect(node.props.className).toBeUndefined()
  })

  it('leaves image icons uncolored (an <img> cannot be recolored)', () => {
    const node = resolveAppIcon('icon.png', 'com.example.test', 14, '#2563eb') as any
    expect(node.type).toBe('img')
    expect(node.props.style).not.toHaveProperty('color')
  })

  it('renders an <img> for a PNG file name', () => {
    const node = resolveAppIcon('icon.png', 'com.example.test', 14) as any
    expect(node.type).toBe('img')
    expect(node.props.src).toBe('http://127.0.0.1:18080/plugin/com.example.test/icon.png')
  })

  it('renders an <img> for an SVG file name', () => {
    const node = resolveAppIcon('logo.svg', 'com.example.test', 20) as any
    expect(node.type).toBe('img')
    expect(node.props.src).toBe('http://127.0.0.1:18080/plugin/com.example.test/logo.svg')
  })

  it('renders an <img> for an absolute HTTP URL', () => {
    const node = resolveAppIcon('https://example.com/icon.png', 'app.test', 14) as any
    expect(node.type).toBe('img')
    expect(node.props.src).toBe('https://example.com/icon.png')
  })

  it('renders an <img> for a path-relative URL', () => {
    const node = resolveAppIcon('/assets/icon.jpg', 'app.test', 14) as any
    expect(node.type).toBe('img')
    expect(node.props.src).toBe('/assets/icon.jpg')
  })

  it('falls back to Puzzle for undefined icon', () => {
    const node = resolveAppIcon(undefined, 'app.test', 14) as any
    expect(node.type).not.toBe('img')
    expect(node.type.displayName ?? node.type.name).toMatch(/Puzzle/)
  })

  it('falls back to Puzzle for unknown lucide name', () => {
    const node = resolveAppIcon('nonexistent-icon', 'app.test', 14) as any
    expect(node.type).not.toBe('img')
    expect(node.type.displayName ?? node.type.name).toMatch(/Puzzle/)
  })

  it('falls back to Puzzle for image without appId', () => {
    const node = resolveAppIcon('icon.png', undefined, 14) as any
    expect(node.type).not.toBe('img')
    expect(node.type.displayName ?? node.type.name).toMatch(/Puzzle/)
  })

  it('supports various image extensions', () => {
    for (const ext of ['.jpg', '.jpeg', '.webp', '.gif', '.ico', '.bmp']) {
      const node = resolveAppIcon(`icon${ext}`, 'app.test', 14) as any
      expect(node.type).toBe('img')
      expect(node.props.src).toContain(`icon${ext}`)
    }
  })
})