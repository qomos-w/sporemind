import { describe, it, expect, vi } from 'vitest'
import type { AppEntry } from '../../application/app-registry'
import { appCardId, createAppCardDescriptor, createAppCardDescriptors, AppCardFallbackBody } from './appCard'
import { PluginIframe } from '../components/PluginIframe'

vi.mock('../components/PluginIframe', () => ({ PluginIframe: () => null }))

function entry(overrides: Partial<AppEntry> = {}): AppEntry {
  return {
    id: 'com.example.notes',
    name: 'Notes',
    runtime: 'native',
    state: 'running',
    version: '1.2.3',
    entrypoints: [{ id: 'main', kind: 'view', title: 'Notes', route: 'index.html' }],
    icon: 'notebook-pen',
    color: '#c9b47f',
    ...overrides,
  }
}

describe('createAppCardDescriptor', () => {
  it('builds host metadata from the registry entry', () => {
    const descriptor = createAppCardDescriptor(entry())!
    expect(descriptor.id).toBe(appCardId('com.example.notes'))
    expect(descriptor.kind).toBe('app')
    expect(descriptor.title).toBe('Notes')
    expect(descriptor.icon).toBe('notebook-pen')
    expect(descriptor.color).toBe('#c9b47f')
    expect(descriptor.compactMeta.statusText).toContain('running')
    expect(descriptor.compactMeta.statusText).toContain('v1.2.3')
  })

  it('falls back to the app id when the manifest has no name', () => {
    const descriptor = createAppCardDescriptor(entry({ name: undefined }))!
    expect(descriptor.title).toBe('com.example.notes')
  })

  it('never mounts the iframe in the compact form and mounts the view when expanded', () => {
    const descriptor = createAppCardDescriptor(entry())!
    expect(descriptor.render(false)).toBeNull()

    const element = descriptor.render(true) as { type: unknown; props: Record<string, unknown> }
    expect(element.type).toBe(PluginIframe)
    expect(element.props).toMatchObject({
      pluginID: 'com.example.notes',
      viewID: 'main',
      route: 'index.html',
      title: 'Notes',
    })
  })

  it('falls back to the first panel entrypoint when no view exists', () => {
    const descriptor = createAppCardDescriptor(
      entry({ entrypoints: [{ id: 'side', kind: 'panel', title: 'Side', zone: 'right', route: '/panel' }] }),
    )!
    expect(descriptor.compactMeta.statusText).toContain('running')

    const element = descriptor.render(true) as { type: unknown; props: Record<string, unknown> }
    expect(element.type).toBe(PluginIframe)
    expect(element.props).toMatchObject({
      pluginID: 'com.example.notes',
      viewID: 'side',
      route: '/panel',
    })
  })

  it('degrades to a metadata-only card when no view or panel entrypoint exists', () => {
    const descriptor = createAppCardDescriptor(entry({ entrypoints: [{ id: 'ping', kind: 'command', title: 'Ping' }] }))!
    expect(descriptor.id).toBe(appCardId('com.example.notes'))
    expect(descriptor.render(false)).toBeNull()

    const element = descriptor.render(true) as { type: unknown; props: Record<string, unknown> }
    expect(element.type).toBe(AppCardFallbackBody)
    expect(element.props).toMatchObject({ state: 'running' })
  })

  it('maps a list of apps: every app gets a card, view or not', () => {
    const descriptors = createAppCardDescriptors([
      entry(),
      entry({ id: 'com.example.panel-only', entrypoints: [{ id: 'side', kind: 'panel', title: 'Side', route: '/p' }] }),
      entry({ id: 'com.example.no-ui', entrypoints: [] }),
    ])
    expect(descriptors.map(d => d.id)).toEqual([
      'app:com.example.notes',
      'app:com.example.panel-only',
      'app:com.example.no-ui',
    ])
  })
})
