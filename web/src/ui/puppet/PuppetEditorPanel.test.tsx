import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '../../i18n'
import type { PuppetDocumentSnapshot } from '../../gen-types/puppet'
import { PuppetEditorPanel } from './PuppetEditorPanel'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const mocks = vi.hoisted(() => ({
  render: vi.fn().mockReturnValue({
    drawnCommands: 1,
    skippedCommands: 0,
    gaps: { unsupportedBlendModes: [], maskCommands: 0, compositeCommands: 0, totalCommands: 1 },
  }),
  loadTextures: vi.fn().mockResolvedValue(undefined),
  dispose: vi.fn(),
  snapshot: vi.fn(),
  edit: vi.fn(),
  readAsset: vi.fn(),
  listAssets: vi.fn().mockResolvedValue({ Assets: [] }),
  commitAsset: vi.fn(),
  rejectAsset: vi.fn(),
  stageAsset: vi.fn(),
}))

vi.mock('../inochi2d/webglRenderer', () => ({
  DrawListWebGLRenderer: class MockRenderer {
    render = mocks.render
    loadTextures = mocks.loadTextures
    dispose = mocks.dispose
  },
}))

vi.mock('../../gen-clients/puppet.document/client', () => ({
  snapshot: mocks.snapshot,
}))

vi.mock('../../gen-clients/puppet/client', () => ({
  edit: mocks.edit,
}))

vi.mock('../../gen-clients/puppet.asset/client', () => ({
  commit: mocks.commitAsset,
  list: mocks.listAssets,
  read: mocks.readAsset,
  reject: mocks.rejectAsset,
  stage: mocks.stageAsset,
}))

function makeSnapshot({ rootOnly = false, name = 'Puppet' }: { rootOnly?: boolean; name?: string } = {}): PuppetDocumentSnapshot {
  return {
    Document: {
      Id: 'doc-1',
      Revision: 1,
      Name: name,
      Root: {
        Guid: 'root-guid',
        Name: 'Ignored root name',
        Kind: 'root',
        Enabled: true,
        Params: { author: 'tester' },
        Children: rootOnly ? [] : [{
          Guid: 'node-guid',
          Name: 'Node',
          Kind: 'node',
          Enabled: false,
          Z: 3,
          Children: [{
            Guid: 'part-guid',
            Name: 'Part',
            Kind: 'part',
            Enabled: true,
            Z: 7,
            TextureAssetId: 'texture-1',
            Mesh: {
              Vertices: [-50, -50, 50, -50, 50, 50, -50, 50],
              Indices: [0, 1, 2, 0, 2, 3],
              UVs: [0, 1, 1, 1, 1, 0, 0, 0],
            },
            Params: { opacity: '0.8', width: '512' },
            Children: [],
          }],
        }],
      },
      CreatedAt: '2026-01-01T00:00:00Z',
      UpdatedAt: '2026-01-01T00:00:00Z',
    },
    StagedAssetCount: 0,
    RevisionCount: 1,
  }
}

describe('PuppetEditorPanel', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    mocks.snapshot.mockReset()
    mocks.edit.mockReset()
    mocks.readAsset.mockReset()
    mocks.listAssets.mockReset().mockResolvedValue({ Assets: [] })
    mocks.commitAsset.mockReset()
    mocks.rejectAsset.mockReset()
    mocks.stageAsset.mockReset()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })

  async function render() {
    await act(async () => {
      root.render(<I18nProvider initialLocale="en-US"><PuppetEditorPanel /></I18nProvider>)
    })
  }

  function setInputValue(input: HTMLInputElement, value: string): void {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  }

  it('shows loading while the document snapshot request is pending', async () => {
    let resolveSnapshot: ((value: { Snapshot: PuppetDocumentSnapshot }) => void) | undefined
    mocks.snapshot.mockReturnValue(new Promise(resolve => { resolveSnapshot = resolve }))

    await render()
    expect(container.textContent).toContain('Loading document...')

    await act(async () => { resolveSnapshot?.({ Snapshot: makeSnapshot() }) })
    expect(container.textContent).toContain('Root')
  })

  it('renders a root-only document without child nodes', async () => {
    mocks.snapshot.mockResolvedValue({ Snapshot: makeSnapshot({ rootOnly: true }) })
    await render()
    await vi.waitFor(() => expect(container.querySelectorAll('[role="treeitem"]')).toHaveLength(1))

    expect(container.textContent).toContain('Root')
    expect(container.textContent).toContain('Guid')
    expect(container.textContent).toContain('root-guid')
    expect(container.textContent).toContain('author')
    expect(container.textContent).toContain('tester')
  })

  it('renders an error when the document snapshot request fails', async () => {
    mocks.snapshot.mockRejectedValue(new Error('backend unavailable'))
    await render()

    await vi.waitFor(() => expect(container.textContent).toContain('Unable to load document: backend unavailable'))
    expect(container.querySelector('[role="tree"]')).toBeNull()
  })

  it('updates the readonly inspector when a tree node is selected', async () => {
    mocks.snapshot.mockResolvedValue({ Snapshot: makeSnapshot() })
    await render()
    await vi.waitFor(() => expect(container.querySelector('[data-selected-guid="root-guid"]')).toBeTruthy())

    const part = [...container.querySelectorAll('button')].find(button => button.textContent?.includes('Part'))
    expect(part).toBeTruthy()
    await act(async () => { part?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })

    expect(container.querySelector('[data-selected-guid="part-guid"]')).toBeTruthy()
    expect(container.textContent).toContain('texture-1')
    expect(container.textContent).toContain('opacity')
    expect(container.textContent).toContain('0.8')
  })

  it('refreshes the snapshot and resets a missing selected node to Root', async () => {
    mocks.snapshot
      .mockResolvedValueOnce({ Snapshot: makeSnapshot() })
      .mockResolvedValueOnce({ Snapshot: makeSnapshot({ rootOnly: true, name: 'Refreshed' }) })
    await render()
    await vi.waitFor(() => expect(container.querySelector('[data-selected-guid="root-guid"]')).toBeTruthy())

    const part = [...container.querySelectorAll('button')].find(button => button.textContent?.includes('Part'))
    await act(async () => { part?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(container.querySelector('[data-selected-guid="part-guid"]')).toBeTruthy()

    const refresh = container.querySelector<HTMLButtonElement>('[aria-label="Refresh document"]')
    expect(refresh).toBeTruthy()
    await act(async () => { refresh?.click() })
    await vi.waitFor(() => expect(mocks.snapshot).toHaveBeenCalledTimes(2))

    expect(container.querySelector('[data-selected-guid="root-guid"]')).toBeTruthy()
    expect(container.querySelector('[data-selected-guid="part-guid"]')).toBeNull()
  })

  // ── Integration: whitelist edit flow ─────────────────────────────────

  it('submits a whitelist name edit and refreshes the document', async () => {
    // First load: original snapshot; second load: refreshed with renamed node.
    mocks.snapshot
      .mockResolvedValueOnce({ Snapshot: makeSnapshot() })
      .mockResolvedValueOnce({ Snapshot: makeSnapshot({ name: 'Renamed' }) })
    mocks.edit.mockResolvedValue({
      Applied: true,
      Revision: {
        Id: 'rev-2', DocumentId: 'doc-1', Sequence: 2, ParentRevisionId: 'rev-1',
        Author: 'tester', CommandKind: 'set_node_name', Timestamp: '2026-01-02T00:00:00Z',
      },
    })

    await render()
    await vi.waitFor(() => expect(container.querySelector('[data-selected-guid="root-guid"]')).toBeTruthy())

    // Select the "Node" child so the Inspector edit form targets it.
    const node = [...container.querySelectorAll('button')].find(b => b.textContent?.includes('Node'))
    expect(node).toBeTruthy()
    await act(async () => { node?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(container.querySelector('[data-selected-guid="node-guid"]')).toBeTruthy()

    // Change the name input and click Save.
    const nameInput = container.querySelector<HTMLInputElement>('input[data-edit-field="name"]')!
    expect(nameInput.value).toBe('Node')
    await act(async () => { setInputValue(nameInput, 'RenamedNode') })

    const saveBtn = container.querySelector<HTMLButtonElement>('button[data-edit-action="set_node_name"]')!
    expect(saveBtn.disabled).toBe(false)
    await act(async () => { saveBtn.click() })

    // Verify the edit callable was invoked with the whitelist command.
    await vi.waitFor(() => expect(mocks.edit).toHaveBeenCalledTimes(1))
    expect(mocks.edit).toHaveBeenCalledWith(expect.anything(), {
      Command: expect.objectContaining({
        Kind: 'set_node_name',
        TargetGuid: 'node-guid',
        Params: { name: 'RenamedNode' },
      }),
    })

    // Verify the snapshot was refreshed (second call).
    await vi.waitFor(() => expect(mocks.snapshot).toHaveBeenCalledTimes(2))
  })

  it('shows an error when the edit is rejected (Applied=false)', async () => {
    mocks.snapshot.mockResolvedValue({ Snapshot: makeSnapshot() })
    mocks.edit.mockResolvedValue({ Applied: false, Revision: {} as any, Detail: 'unknown command' })

    await render()
    await vi.waitFor(() => expect(container.querySelector('[data-selected-guid="root-guid"]')).toBeTruthy())

    // Change the Z input and click Save.
    const zInput = container.querySelector<HTMLInputElement>('input[data-edit-field="z"]')!
    await act(async () => { setInputValue(zInput, '99') })

    const saveBtn = container.querySelector<HTMLButtonElement>('button[data-edit-action="set_node_z"]')!
    await act(async () => { saveBtn.click() })

    await vi.waitFor(() => expect(container.querySelector('[role="alert"]')?.textContent).toContain('unknown command'))
    // Snapshot should not be refreshed on rejection.
    expect(mocks.snapshot).toHaveBeenCalledTimes(1)
  })

  it('shows an error when the edit call throws', async () => {
    mocks.snapshot.mockResolvedValue({ Snapshot: makeSnapshot() })
    mocks.edit.mockRejectedValue(new Error('network failure'))

    await render()
    await vi.waitFor(() => expect(container.querySelector('[data-selected-guid="root-guid"]')).toBeTruthy())

    // Change the name input and Save; the mock throws.
    const nameInput = container.querySelector<HTMLInputElement>('input[data-edit-field="name"]')!
    await act(async () => { setInputValue(nameInput, 'ThrowName') })

    const saveBtn = container.querySelector<HTMLButtonElement>('button[data-edit-action="set_node_name"]')!
    expect(saveBtn.disabled).toBe(false)
    await act(async () => { saveBtn.click() })

    await vi.waitFor(() => expect(mocks.edit).toHaveBeenCalledTimes(1))
    await vi.waitFor(() => expect(container.textContent).toContain('network failure'))
    // Snapshot should not be refreshed when the call throws.
    expect(mocks.snapshot).toHaveBeenCalledTimes(1)
  })

  it('disables save buttons until the field value differs from the committed value', async () => {
    mocks.snapshot.mockResolvedValue({ Snapshot: makeSnapshot() })
    await render()
    await vi.waitFor(() => expect(container.querySelector('[data-selected-guid="root-guid"]')).toBeTruthy())

    // root-guid has Enabled=true, so the save-enabled button is disabled until toggled.
    const enabledBtn = container.querySelector<HTMLButtonElement>('button[data-edit-action="set_node_enabled"]')!
    expect(enabledBtn.disabled).toBe(true)

    const checkbox = container.querySelector<HTMLInputElement>('input[data-edit-field="enabled"]')!
    await act(async () => { checkbox.click() })
    expect(enabledBtn.disabled).toBe(false)

    // Toggle back → disabled again.
    await act(async () => { checkbox.click() })
    expect(enabledBtn.disabled).toBe(true)
  })

  // ── Integration: texture loading & mesh summary ──────────────────────

  it('loads node textures via puppet.asset.read after the snapshot loads', async () => {
    mocks.snapshot.mockResolvedValue({ Snapshot: makeSnapshot() })
    mocks.readAsset.mockResolvedValue({
      AssetId: 'texture-1',
      Uri: 'data:image/png;base64,AAAA',
      MimeType: 'image/png',
      Width: 64,
      Height: 64,
    })

    await render()

    await vi.waitFor(() => expect(mocks.readAsset).toHaveBeenCalledTimes(1))
    expect(mocks.readAsset).toHaveBeenCalledWith(expect.anything(), { AssetId: 'texture-1' })
  })

  it('survives asset read failures without breaking the document view', async () => {
    mocks.snapshot.mockResolvedValue({ Snapshot: makeSnapshot() })
    mocks.readAsset.mockRejectedValue(new Error('asset not committed'))

    await render()
    await vi.waitFor(() => expect(mocks.readAsset).toHaveBeenCalledTimes(1))

    // The document still renders; the read failure must not surface as a load error.
    expect(container.querySelector('[data-selected-guid="root-guid"]')).toBeTruthy()
    expect(container.textContent).not.toContain('Failed to load document')
  })

  it('shows the mesh summary (vertices / triangles) for a mesh node', async () => {
    mocks.snapshot.mockResolvedValue({ Snapshot: makeSnapshot() })
    await render()
    await vi.waitFor(() => expect(container.querySelector('[data-selected-guid="root-guid"]')).toBeTruthy())

    const part = [...container.querySelectorAll('button')].find(button => button.textContent?.includes('Part'))
    expect(part).toBeTruthy()
    await act(async () => { part?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })

    expect(container.querySelector('[data-selected-guid="part-guid"]')).toBeTruthy()
    expect(container.textContent).toContain('Mesh')
    expect(container.textContent).toContain('4 vertices · 2 triangles')
  })
})
