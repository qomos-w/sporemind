import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createRoot } from 'react-dom/client'
import { act } from 'react'
import { FilePicker } from './FilePicker'
import { I18nProvider } from '../../../i18n'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const rootsMock = vi.fn()
const listJsonMock = vi.fn()

vi.mock('../../../application/generated-client', () => ({ client: {} }))
vi.mock('../../../application/wails-bridge', () => ({
  hasWailsBindings: () => false,
  selectFolder: vi.fn(async () => ''),
}))
vi.mock('../../../gen-clients/filesystem/client', () => ({
  roots: (...args: unknown[]) => rootsMock(...args),
  listJson: (...args: unknown[]) => listJsonMock(...args),
}))

function dirEntry(name: string) {
  return { Name: name, IsDir: true, Size: 0, ModTime: '' }
}

function renderPicker(props: Partial<Parameters<typeof FilePicker>[0]> = {}) {
  const onSelect = vi.fn()
  const onCancel = vi.fn()
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  act(() => {
    root.render(
      <I18nProvider initialLocale="en-US">
        <FilePicker
          open
          mode="folder"
          onSelect={onSelect}
          onCancel={onCancel}
          {...props}
        />
      </I18nProvider>
    )
  })
  return { container, root, onSelect, onCancel }
}

async function flush() {
  await act(async () => {})
}

beforeEach(() => {
  rootsMock.mockReset()
  listJsonMock.mockReset()
  rootsMock.mockResolvedValue({ Home: 'C:\\Users\\dev', Roots: ['C:\\', 'D:\\'] })
})

describe('FilePicker web fallback', () => {
  it('opens in the roots view: home + volume roots from filesystem.roots', async () => {
    const { container } = renderPicker()
    await flush()
    expect(rootsMock).toHaveBeenCalledTimes(1)
    const names = Array.from(container.querySelectorAll('.fp-item-name')).map(el => el.textContent)
    expect(names).toEqual(['C:\\Users\\dev', 'C:\\', 'D:\\'])
    const up = container.querySelector<HTMLButtonElement>('.fp-up-btn')
    expect(up?.disabled).toBe(true)
    const select = container.querySelector<HTMLButtonElement>('.fp-select-btn')
    expect(select?.disabled).toBe(true)
  })

  it('navigates into a volume root and back up to the roots view', async () => {
    listJsonMock.mockResolvedValue({ Items: [dirEntry('docs')], NumEntries: 1 })
    const { container } = renderPicker()
    await flush()

    const drive = Array.from(container.querySelectorAll('.fp-item')).find(
      b => b.textContent?.includes('D:\\')
    )!
    await act(async () => { drive.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flush()
    expect(listJsonMock).toHaveBeenCalledWith(expect.anything(), { Path: 'D:\\' })

    await act(async () => {
      container.querySelector<HTMLButtonElement>('.fp-up-btn')!
        .dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flush()
    // At the drive root the parent is the roots view, not a relative "D:".
    const names = Array.from(container.querySelectorAll('.fp-item-name')).map(el => el.textContent)
    expect(names).toEqual(['C:\\Users\\dev', 'C:\\', 'D:\\'])
  })

  it('computes a slash-terminated parent for direct drive-root children', async () => {
    listJsonMock.mockResolvedValue({ Items: [], NumEntries: 0 })
    const { container } = renderPicker()
    await flush()

    const home = Array.from(container.querySelectorAll('.fp-item')).find(
      b => b.textContent?.includes('C:\\Users\\dev')
    )!
    await act(async () => { home.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flush()

    const up = () =>
      act(async () => {
        container.querySelector<HTMLButtonElement>('.fp-up-btn')!
          .dispatchEvent(new MouseEvent('click', { bubbles: true }))
      })

    await up() // C:\Users\dev -> C:\Users
    await flush()
    await up() // C:\Users -> C:/ (never the drive-relative "C:")
    await flush()
    const calls = listJsonMock.mock.calls.map(c => c[1].Path)
    expect(calls).toEqual(['C:\\Users\\dev', 'C:\\Users', 'C:/'])
  })

  it('falls back to the server default root when filesystem.roots fails', async () => {
    rootsMock.mockRejectedValue(new Error('unavailable'))
    listJsonMock.mockResolvedValue({ Items: [], NumEntries: 0 })
    renderPicker()
    await flush()
    expect(listJsonMock).toHaveBeenCalledWith(expect.anything(), { Path: '' })
  })
})
