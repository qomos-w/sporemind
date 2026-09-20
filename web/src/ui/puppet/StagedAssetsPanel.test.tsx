import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '../../i18n'
import type { PuppetNode, PuppetRevision, PuppetStagedAsset } from '../../gen-types/puppet'
import { StagedAssetsPanel } from './StagedAssetsPanel'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const mocks = vi.hoisted(() => ({
  list: vi.fn(),
  commit: vi.fn(),
  reject: vi.fn(),
  log: vi.fn(),
}))

vi.mock('../../gen-clients/puppet.asset/client', () => ({
  list: mocks.list,
  commit: mocks.commit,
  reject: mocks.reject,
  stage: vi.fn(),
}))

vi.mock('../../gen-clients/puppet.revision/client', () => ({
  log: mocks.log,
}))

// React controlled-input value setter helper (the happy-dom + React gotcha).
function setInputValue(input: HTMLInputElement, value: string): void {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

function makeRoot(): PuppetNode {
  return {
    Guid: 'root-guid',
    Name: 'Root',
    Kind: 'group',
    Enabled: true,
    Children: [
      { Guid: 'part-1', Name: 'Body', Kind: 'part', Enabled: true, Z: 1, Children: [] },
      { Guid: 'part-2', Name: 'Head', Kind: 'part', Enabled: true, Z: 2, Children: [] },
    ],
  }
}

function makeAsset(overrides: Partial<PuppetStagedAsset> = {}): PuppetStagedAsset {
  return {
    Id: 'asset-1',
    DocumentId: 'doc-1',
    State: 'staged',
    Kind: 'texture',
    DataRef: 'data:image/png;base64,iVBORw0KGgo=',
    Name: 'Generated Texture',
    SourceRef: 'gen-req-1',
    CreatedAt: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

function makeRevision(overrides: Partial<PuppetRevision> = {}): PuppetRevision {
  return {
    Id: 'rev-1',
    DocumentId: 'doc-1',
    Sequence: 1,
    ParentRevisionId: '',
    Author: 'tester',
    CommandKind: 'init',
    Timestamp: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

function findActionBtn(container: HTMLElement, state: string, label: string): HTMLButtonElement {
  const article = container.querySelector(`[data-asset-state="${state}"]`)!
  return [...article.querySelectorAll('button')].find(
    b => b.getAttribute('aria-label')?.startsWith(label),
  )! as HTMLButtonElement
}

describe('StagedAssetsPanel', () => {
  let container: HTMLDivElement
  let root: Root
  const onRefreshDocument = vi.fn().mockResolvedValue(undefined)

  beforeEach(() => {
    mocks.list.mockReset()
    mocks.commit.mockReset()
    mocks.reject.mockReset()
    mocks.log.mockReset()
    onRefreshDocument.mockReset().mockResolvedValue(undefined)
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })

  async function render(props?: { root?: PuppetNode; onRefreshDocument?: () => Promise<void> }) {
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <StagedAssetsPanel
            root={props?.root ?? makeRoot()}
            onRefreshDocument={props?.onRefreshDocument ?? onRefreshDocument}
          />
        </I18nProvider>,
      )
    })
  }

  // staged / committed / rejected 三态
  it('renders staged, committed, and rejected assets with their states and details', async () => {
    mocks.list.mockResolvedValue({
      Assets: [
        makeAsset({ Id: 'a-staged', State: 'staged' }),
        makeAsset({ Id: 'a-committed', State: 'committed', NodeGuid: 'part-1', ReviewedAt: '2026-01-02T00:00:00Z' }),
        makeAsset({ Id: 'a-rejected', State: 'rejected', ReviewReason: 'wrong color' }),
      ],
    })
    mocks.log.mockResolvedValue({ Revisions: [makeRevision()] })

    await render()
    await vi.waitFor(() => expect(container.querySelectorAll('.puppet-asset')).toHaveLength(3))

    const staged = container.querySelector('[data-asset-state="staged"]')!
    const committed = container.querySelector('[data-asset-state="committed"]')!
    const rejected = container.querySelector('[data-asset-state="rejected"]')!

    expect(staged.querySelector('.puppet-asset-state')!.textContent).toBe('staged')
    expect(committed.querySelector('.puppet-asset-state')!.textContent).toBe('committed')
    expect(rejected.querySelector('.puppet-asset-state')!.textContent).toBe('rejected')

    // thumbnail + source + created rendered
    expect(staged.querySelector('.puppet-asset-thumbnail img')).toBeTruthy()
    expect(staged.textContent).toContain('Source')
    expect(staged.textContent).toContain('gen-req-1')
    expect(staged.textContent).toContain('Created')
    expect(committed.textContent).toContain('part-1')
    expect(rejected.textContent).toContain('wrong color')

    // only staged assets expose review actions
    expect(staged.querySelector('.puppet-asset-actions')).toBeTruthy()
    expect(committed.querySelector('.puppet-asset-actions')).toBeNull()
    expect(rejected.querySelector('.puppet-asset-actions')).toBeNull()
  })

  it('shows empty states when there are no assets and no revisions', async () => {
    mocks.list.mockResolvedValue({ Assets: [] })
    mocks.log.mockResolvedValue({ Revisions: [] })

    await render()
    await vi.waitFor(() => expect(container.textContent).toContain('No staged assets'))
    expect(container.textContent).toContain('No revisions')
  })

  // 非法目标: commit blocked until a valid target node is selected
  it('disables the commit button until a target node is selected', async () => {
    mocks.list.mockResolvedValue({ Assets: [makeAsset()] })
    mocks.log.mockResolvedValue({ Revisions: [makeRevision()] })

    await render()
    await vi.waitFor(() => expect(container.querySelector('[data-asset-state="staged"]')).toBeTruthy())

    const commitBtn = findActionBtn(container, 'staged', 'Commit asset')
    expect(commitBtn.disabled).toBe(true)

    const select = container.querySelector<HTMLSelectElement>('select[aria-label="Target node"]')!
    await act(async () => {
      select.value = 'part-1'
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(commitBtn.disabled).toBe(false)
  })

  // 失败提示: backend rejects the commit (e.g. illegal/unknown target node)
  it('shows a failure notice and does not refresh when commit is rejected by the backend', async () => {
    mocks.list.mockResolvedValue({ Assets: [makeAsset({ Id: 'a-1' })] })
    mocks.log.mockResolvedValue({ Revisions: [makeRevision()] })
    mocks.commit.mockRejectedValue(new Error('node not found'))

    await render()
    await vi.waitFor(() => expect(container.querySelector('[data-asset-state="staged"]')).toBeTruthy())

    const select = container.querySelector<HTMLSelectElement>('select[aria-label="Target node"]')!
    await act(async () => {
      select.value = 'part-1'
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const commitBtn = findActionBtn(container, 'staged', 'Commit asset')
    await act(async () => { commitBtn.click() })

    await vi.waitFor(() => expect(container.querySelector('[role="alert"]')?.textContent).toContain('node not found'))
    expect(onRefreshDocument).not.toHaveBeenCalled()
  })

  // 失败提示: reject fails
  it('shows a failure notice when reject is rejected by the backend', async () => {
    mocks.list.mockResolvedValue({ Assets: [makeAsset()] })
    mocks.log.mockResolvedValue({ Revisions: [makeRevision()] })
    mocks.reject.mockRejectedValue(new Error('already reviewed'))

    await render()
    await vi.waitFor(() => expect(container.querySelector('[data-asset-state="staged"]')).toBeTruthy())

    const reasonInput = container.querySelector<HTMLInputElement>('input[aria-label="Rejection reason"]')!
    await act(async () => { setInputValue(reasonInput, 'low quality') })

    const rejectBtn = findActionBtn(container, 'staged', 'Reject asset')
    await act(async () => { rejectBtn.click() })

    await vi.waitFor(() => expect(container.querySelector('[role="alert"]')?.textContent).toContain('already reviewed'))
  })

  // 成功后 revision 更新
  it('refreshes snapshot, assets, and revision log after a successful commit', async () => {
    const initRevision = makeRevision({ Sequence: 1, CommandKind: 'init' })
    const commitRevision = makeRevision({ Id: 'rev-2', Sequence: 2, CommandKind: 'commit_asset' })

    mocks.list
      .mockResolvedValueOnce({ Assets: [makeAsset({ Id: 'a-1', State: 'staged' })] })
      .mockResolvedValueOnce({ Assets: [makeAsset({ Id: 'a-1', State: 'committed', NodeGuid: 'part-1', ReviewedAt: '2026-01-02T00:00:00Z' })] })
    mocks.log
      .mockResolvedValueOnce({ Revisions: [initRevision] })
      .mockResolvedValueOnce({ Revisions: [commitRevision, initRevision] })
    mocks.commit.mockResolvedValue({
      Asset: makeAsset({ Id: 'a-1', State: 'committed', NodeGuid: 'part-1' }),
      Revision: commitRevision,
    })

    await render()
    await vi.waitFor(() => expect(container.querySelector('[data-asset-state="staged"]')).toBeTruthy())

    const select = container.querySelector<HTMLSelectElement>('select[aria-label="Target node"]')!
    await act(async () => {
      select.value = 'part-1'
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const commitBtn = findActionBtn(container, 'staged', 'Commit asset')
    await act(async () => { commitBtn.click() })

    await vi.waitFor(() => expect(mocks.commit).toHaveBeenCalled())
    expect(mocks.commit).toHaveBeenCalledWith(expect.anything(), { AssetId: 'a-1', NodeGuid: 'part-1' })

    // snapshot refreshed through the parent callback
    await vi.waitFor(() => expect(onRefreshDocument).toHaveBeenCalled())
    // revision log updated to include the new commit revision
    await vi.waitFor(() => expect(container.textContent).toContain('#2 commit_asset'))
    // asset left the staged state and is now committed
    expect(container.querySelector('[data-asset-state="staged"]')).toBeNull()
    expect(container.querySelector('[data-asset-state="committed"]')).toBeTruthy()
  })

  it('requires a rejection reason before rejecting', async () => {
    mocks.list.mockResolvedValue({ Assets: [makeAsset()] })
    mocks.log.mockResolvedValue({ Revisions: [makeRevision()] })

    await render()
    await vi.waitFor(() => expect(container.querySelector('[data-asset-state="staged"]')).toBeTruthy())

    const rejectBtn = findActionBtn(container, 'staged', 'Reject asset')
    expect(rejectBtn.disabled).toBe(true)

    const reasonInput = container.querySelector<HTMLInputElement>('input[aria-label="Rejection reason"]')!
    await act(async () => { setInputValue(reasonInput, 'too dark') })
    expect(rejectBtn.disabled).toBe(false)
  })

  it('refreshes after a successful reject', async () => {
    mocks.list
      .mockResolvedValueOnce({ Assets: [makeAsset({ Id: 'a-1', State: 'staged' })] })
      .mockResolvedValueOnce({ Assets: [makeAsset({ Id: 'a-1', State: 'rejected', ReviewReason: 'too dark', ReviewedAt: '2026-01-02T00:00:00Z' })] })
    mocks.log.mockResolvedValue({ Revisions: [makeRevision()] })
    mocks.reject.mockResolvedValue({
      Asset: makeAsset({ Id: 'a-1', State: 'rejected', ReviewReason: 'too dark' }),
    })

    await render()
    await vi.waitFor(() => expect(container.querySelector('[data-asset-state="staged"]')).toBeTruthy())

    const reasonInput = container.querySelector<HTMLInputElement>('input[aria-label="Rejection reason"]')!
    await act(async () => { setInputValue(reasonInput, 'too dark') })

    const rejectBtn = findActionBtn(container, 'staged', 'Reject asset')
    await act(async () => { rejectBtn.click() })

    await vi.waitFor(() => expect(mocks.reject).toHaveBeenCalledWith(expect.anything(), { AssetId: 'a-1', Reason: 'too dark' }))
    await vi.waitFor(() => expect(onRefreshDocument).toHaveBeenCalled())
    expect(container.querySelector('[data-asset-state="rejected"]')).toBeTruthy()
    expect(container.querySelector('[data-asset-state="staged"]')).toBeNull()
  })
})
