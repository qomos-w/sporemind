import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import type { GosporeClient } from '@qomos/gospore-client'
import { EditorView, type ViewPlugin, type PluginValue } from '@codemirror/view'
import { EditorState } from '@codemirror/state'
import { javascript } from '@codemirror/lang-javascript'
import { ensureSyntaxTree } from '@codemirror/language'
import { lspGotoDefinition, LSP_REQUEST_BUDGET_MS, type LspGotoDefinitionOptions } from './lspGotoDefinition'
import { LspClient } from './lspClient'

describe('lspGotoDefinition', () => {
  let container: HTMLElement
  let onGotoDefinition: ReturnType<typeof vi.fn<(filePath: string, line: number) => void>>
  let client: LspClient
  let options: LspGotoDefinitionOptions

  beforeEach(() => {
    // Polyfills required by CodeMirror's animation frame scheduling in test env.
    globalThis.requestAnimationFrame = (cb) => setTimeout(cb, 0) as unknown as number
    globalThis.cancelAnimationFrame = (id) => clearTimeout(id)

    container = document.createElement('div')
    document.body.appendChild(container)
    onGotoDefinition = vi.fn<(filePath: string, line: number) => void>()

    client = new LspClient({ client: { invoke: vi.fn(async () => ({ Json: '' })) } as unknown as GosporeClient })
    vi.spyOn(client, 'definition').mockResolvedValue({
      uri: 'file:///target/file.ts',
      range: { start: { line: 42, character: 0 }, end: { line: 42, character: 10 } },
    })

    options = {
      client,
      filePath: '/source/file.ts',
      onGotoDefinition,
    }
  })

  afterEach(() => {
    container.remove()
  })

  function createView(code: string) {
    const extension = lspGotoDefinition(options)
    const state = EditorState.create({
      doc: code,
      extensions: [javascript({ typescript: true }), extension],
    })
    const view = new EditorView({ state, parent: container })
    // Ensure the Lezer syntax tree is available synchronously after creation.
    ensureSyntaxTree(state, 1000)
    return { view, extension }
  }

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  function pluginInstance(view: EditorView, extension: ReturnType<typeof lspGotoDefinition>) {
    const plugin = view.plugin(extension as unknown as ViewPlugin<PluginValue & Record<string, any>>)
    expect(plugin).toBeTruthy()
    return plugin!
  }

  function mockMouseEvent(overrides: { ctrlKey: boolean; clientX?: number; clientY?: number }): MouseEvent {
    return {
      ctrlKey: overrides.ctrlKey,
      clientX: overrides.clientX ?? 10,
      clientY: overrides.clientY ?? 10,
      preventDefault: vi.fn(),
    } as unknown as MouseEvent
  }

  it('underlines the identifier under the cursor when Ctrl is held', () => {
    const { view, extension } = createView('const x = 42')
    view.posAtCoords = vi.fn(() => 7) as typeof view.posAtCoords

    const plugin = pluginInstance(view, extension)
    plugin.handleMouseMove(mockMouseEvent({ ctrlKey: true }))

    expect(plugin.ctrlHeld).toBe(true)
    expect(plugin.hoverPos).toBe(7)
    expect(plugin.decorations.size).toBeGreaterThan(0)
    expect(plugin.decorations.size).toBe(1)

    const underlined = view.dom.querySelector('[style*="text-decoration: underline"]')
    expect(underlined).not.toBeNull()
  })

  it('does not underline when Ctrl is not held', () => {
    const { view, extension } = createView('const x = 42')
    view.posAtCoords = vi.fn(() => 7) as typeof view.posAtCoords

    const plugin = pluginInstance(view, extension)
    plugin.handleMouseMove(mockMouseEvent({ ctrlKey: false }))

    expect(plugin.decorations.size).toBe(0)
  })

  it('does not underline over non-identifier content', () => {
    const { view, extension } = createView('const x = 42')
    view.posAtCoords = vi.fn(() => 4) as typeof view.posAtCoords

    const plugin = pluginInstance(view, extension)
    plugin.handleMouseMove(mockMouseEvent({ ctrlKey: true }))

    expect(plugin.decorations.size).toBe(0)
  })

  it('calls definition and onGotoDefinition on Ctrl+Click', async () => {
    const { view, extension } = createView('const x = 42')
    const plugin = pluginInstance(view, extension)
    view.posAtCoords = vi.fn(() => 7) as typeof view.posAtCoords

    plugin.handleKeyToggle({ key: 'Control' } as KeyboardEvent, true)
    await plugin.handleClick(mockMouseEvent({ ctrlKey: true }))

    expect(client.definition).toHaveBeenCalledWith('/source/file.ts', 0, 7)
    expect(onGotoDefinition).toHaveBeenCalledWith('/target/file.ts', 42)
  })

  it('does not trigger goto on click without Ctrl', async () => {
    const { view, extension } = createView('const x = 42')
    const plugin = pluginInstance(view, extension)
    view.posAtCoords = vi.fn(() => 7) as typeof view.posAtCoords

    await plugin.handleClick(mockMouseEvent({ ctrlKey: false }))

    expect(client.definition).not.toHaveBeenCalled()
    expect(onGotoDefinition).not.toHaveBeenCalled()
  })

  it('passes non-file URIs through to the callback', async () => {
    const { view, extension } = createView('const x = 42')
    vi.spyOn(client, 'definition').mockResolvedValue({
      uri: 'untitled:Untitled-1',
      range: { start: { line: 3, character: 0 }, end: { line: 3, character: 1 } },
    })

    const plugin = pluginInstance(view, extension)
    view.posAtCoords = vi.fn(() => 7) as typeof view.posAtCoords

    plugin.handleKeyToggle({ key: 'Control' } as KeyboardEvent, true)
    await plugin.handleClick(mockMouseEvent({ ctrlKey: true }))

    expect(client.definition).toHaveBeenCalledWith('/source/file.ts', 0, 7)
    expect(onGotoDefinition).toHaveBeenCalledWith('untitled:Untitled-1', 3)
  })

  it('shows references when clicking on the definition itself', async () => {
    const onShowReferences = vi.fn<
      (locations: unknown[], anchor: { x: number; y: number }, queryPos: { uri: string; line: number; character: number }) => void
    >()
    options.onShowReferences = onShowReferences
    const { view, extension } = createView('const x = 42')
    // Definition resolves to the same file and same line as the click.
    vi.spyOn(client, 'definition').mockResolvedValue({
      uri: 'file:///source/file.ts',
      range: { start: { line: 0, character: 0 }, end: { line: 0, character: 10 } },
    })
    vi.spyOn(client, 'references').mockResolvedValue([
      { uri: 'file:///source/file.ts', range: { start: { line: 0, character: 0 }, end: { line: 0, character: 10 } } },
      { uri: 'file:///other/use.ts', range: { start: { line: 5, character: 2 }, end: { line: 5, character: 12 } } },
    ])

    const plugin = pluginInstance(view, extension)
    view.posAtCoords = vi.fn(() => 7) as typeof view.posAtCoords

    plugin.handleKeyToggle({ key: 'Control' } as KeyboardEvent, true)
    await plugin.handleClick(mockMouseEvent({ ctrlKey: true, clientX: 100, clientY: 200 }))

    expect(client.references).toHaveBeenCalledWith('/source/file.ts', 0, 7)
    expect(onShowReferences).toHaveBeenCalledWith(
      [
        { uri: 'file:///source/file.ts', range: { start: { line: 0, character: 0 }, end: { line: 0, character: 10 } } },
        { uri: 'file:///other/use.ts', range: { start: { line: 5, character: 2 }, end: { line: 5, character: 12 } } },
      ],
      { x: 100, y: 200 },
      { uri: 'file:///source/file.ts', line: 0, character: 7 },
    )
    expect(onGotoDefinition).not.toHaveBeenCalled()
  })

  it('still jumps when clicking a call site (definition elsewhere)', async () => {
    const onShowReferences = vi.fn()
    options.onShowReferences = onShowReferences
    const { view, extension } = createView('const x = 42')
    vi.spyOn(client, 'references').mockResolvedValue([])
    // Definition resolves to a different file.
    vi.spyOn(client, 'definition').mockResolvedValue({
      uri: 'file:///target/def.ts',
      range: { start: { line: 10, character: 0 }, end: { line: 10, character: 5 } },
    })

    const plugin = pluginInstance(view, extension)
    view.posAtCoords = vi.fn(() => 7) as typeof view.posAtCoords

    plugin.handleKeyToggle({ key: 'Control' } as KeyboardEvent, true)
    await plugin.handleClick(mockMouseEvent({ ctrlKey: true }))

    expect(client.references).not.toHaveBeenCalled()
    expect(onShowReferences).not.toHaveBeenCalled()
    expect(onGotoDefinition).toHaveBeenCalledWith('/target/def.ts', 10)
  })

  it('does not show references when onShowReferences is not provided', async () => {
    const { view, extension } = createView('const x = 42')
    vi.spyOn(client, 'references').mockResolvedValue([])
    vi.spyOn(client, 'definition').mockResolvedValue({
      uri: 'file:///source/file.ts',
      range: { start: { line: 0, character: 0 }, end: { line: 0, character: 10 } },
    })

    const plugin = pluginInstance(view, extension)
    view.posAtCoords = vi.fn(() => 7) as typeof view.posAtCoords

    plugin.handleKeyToggle({ key: 'Control' } as KeyboardEvent, true)
    await plugin.handleClick(mockMouseEvent({ ctrlKey: true }))

    expect(client.references).not.toHaveBeenCalled()
    expect(onGotoDefinition).toHaveBeenCalledWith('/source/file.ts', 0)
  })

  it('swallows errors when LSP connect is rejected (service disabled)', async () => {
    const { view, extension } = createView('const x = 42')
    vi.spyOn(client, 'definition').mockRejectedValue(new Error('lsp.initialize: service disabled'))

    const plugin = pluginInstance(view, extension)
    view.posAtCoords = vi.fn(() => 7) as typeof view.posAtCoords

    plugin.handleKeyToggle({ key: 'Control' } as KeyboardEvent, true)
    // Must not throw — the rejection is swallowed so ctrl+click is a no-op.
    await expect(plugin.handleClick(mockMouseEvent({ ctrlKey: true }))).resolves.toBeUndefined()
    expect(onGotoDefinition).not.toHaveBeenCalled()
  })

  it('shows a busy chip while the definition request is in flight and removes it after', async () => {
    const { view, extension } = createView('const x = 42')
    let resolveDef!: (v: { uri: string; range: { start: { line: number; character: number }; end: { line: number; character: number } } }) => void
    vi.spyOn(client, 'definition').mockImplementation(
      () => new Promise((res) => { resolveDef = res }),
    )

    const plugin = pluginInstance(view, extension)
    view.posAtCoords = vi.fn(() => 7) as typeof view.posAtCoords
    plugin.handleKeyToggle({ key: 'Control' } as KeyboardEvent, true)

    const click = plugin.handleClick(mockMouseEvent({ ctrlKey: true }))
    await Promise.resolve() // let handleClick reach the await
    expect(view.dom.querySelector('.cm-lsp-busy')).not.toBeNull()
    expect(view.dom.classList.contains('cm-lsp-busy-cursor')).toBe(true)

    resolveDef({
      uri: 'file:///target/file.ts',
      range: { start: { line: 42, character: 0 }, end: { line: 42, character: 3 } },
    })
    await click

    expect(view.dom.querySelector('.cm-lsp-busy')).toBeNull()
    expect(view.dom.classList.contains('cm-lsp-busy-cursor')).toBe(false)
    expect(onGotoDefinition).toHaveBeenCalledWith('/target/file.ts', 42)
  })

  it('removes the busy chip when the request fails', async () => {
    const { view, extension } = createView('const x = 42')
    vi.spyOn(client, 'definition').mockRejectedValue(new Error('engine down'))

    const plugin = pluginInstance(view, extension)
    view.posAtCoords = vi.fn(() => 7) as typeof view.posAtCoords
    plugin.handleKeyToggle({ key: 'Control' } as KeyboardEvent, true)

    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    await plugin.handleClick(mockMouseEvent({ ctrlKey: true }))
    warn.mockRestore()

    expect(view.dom.querySelector('.cm-lsp-busy')).toBeNull()
    expect(view.dom.classList.contains('cm-lsp-busy-cursor')).toBe(false)
  })

  it('ignores a second ctrl+click while a request is already in flight', async () => {
    const { view, extension } = createView('const x = 42')
    let resolveDef!: () => void
    vi.spyOn(client, 'definition').mockImplementation(
      () => new Promise((res) => { resolveDef = () => res({
        uri: 'file:///target/file.ts',
        range: { start: { line: 42, character: 0 }, end: { line: 42, character: 3 } },
      }) }),
    )

    const plugin = pluginInstance(view, extension)
    view.posAtCoords = vi.fn(() => 7) as typeof view.posAtCoords
    plugin.handleKeyToggle({ key: 'Control' } as KeyboardEvent, true)

    const first = plugin.handleClick(mockMouseEvent({ ctrlKey: true }))
    await Promise.resolve()
    const second = plugin.handleClick(mockMouseEvent({ ctrlKey: true }))
    await second

    expect(client.definition).toHaveBeenCalledTimes(1)
    expect(plugin.pending).toBe(true)

    resolveDef()
    await first
    expect(plugin.pending).toBe(false)
    expect(onGotoDefinition).toHaveBeenCalledTimes(1)
  })

  it('clears the busy chip when the plugin is destroyed mid-flight', async () => {
    vi.useFakeTimers()
    try {
      const { view, extension } = createView('const x = 42')
      vi.spyOn(client, 'definition').mockImplementation(() => new Promise(() => {}))

      const plugin = pluginInstance(view, extension)
      view.posAtCoords = vi.fn(() => 7) as typeof view.posAtCoords
      plugin.handleKeyToggle({ key: 'Control' } as KeyboardEvent, true)

      const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
      const click = plugin.handleClick(mockMouseEvent({ ctrlKey: true }))
      await vi.advanceTimersByTimeAsync(0)
      expect(view.dom.querySelector('.cm-lsp-busy')).not.toBeNull()

      plugin.destroy?.()
      expect(view.dom.querySelector('.cm-lsp-busy')).toBeNull()
      expect(view.dom.classList.contains('cm-lsp-busy-cursor')).toBe(false)

      // Budget race releases the guard even though the LSP promise never settles.
      await vi.advanceTimersByTimeAsync(LSP_REQUEST_BUDGET_MS)
      await click
      expect(plugin.pending).toBe(false)
      warn.mockRestore()
    } finally {
      vi.useRealTimers()
    }
  })
})