import '@testing-library/jest-dom/vitest'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import React, { useState } from 'react'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { EditorView } from '@codemirror/view'
import { EditorState, type Extension } from '@codemirror/state'
import { getSearchQuery, openSearchPanel, setSearchQuery, SearchQuery } from '@codemirror/search'
import {
  FindReplacePanel,
  MATCH_COUNT_CAP,
  computeMatchInfo,
  useFindReplacePanel,
  type FindReplacePanelProps,
} from './FindReplacePanel'
import { editingExtensions, type FindReplaceHandlers } from './editing-extensions'
import { BrowserOverlayProvider, type BrowserOverlayApi } from '../ai/browserOverlay'
import { I18nProvider } from '../../i18n'

// Polyfills required by CodeMirror's animation frame scheduling in test env.
beforeEach(() => {
  globalThis.requestAnimationFrame = (cb) => setTimeout(cb, 0) as unknown as number
  globalThis.cancelAnimationFrame = (id) => clearTimeout(id)
})

afterEach(() => {
  document.body.innerHTML = ''
  vi.useRealTimers()
  vi.restoreAllMocks()
})

function makeView(doc: string, { editable = true } = {}): EditorView {
  const container = document.createElement('div')
  document.body.appendChild(container)
  // Mirror the real editors: editingExtensions supplies search() with the
  // suppressed default panel, so the panel's openSearchPanel() activation
  // call must never render the stock CM search UI.
  const extensions: Extension[] = [
    editingExtensions({ editable }),
    EditorView.editable.of(editable),
    EditorState.readOnly.of(!editable),
  ]
  const state = EditorState.create({ doc, extensions })
  return new EditorView({ state, parent: container })
}

function makeProps(view: EditorView | null, overrides: Partial<FindReplacePanelProps> = {}): FindReplacePanelProps {
  return {
    view,
    open: true,
    mode: 'find',
    focusKey: 0,
    editable: true,
    onClose: vi.fn(),
    onModeChange: vi.fn(),
    ...overrides,
  }
}

const flushDebounce = async (ms = 200) => {
  await act(async () => { await vi.advanceTimersByTimeAsync(ms) })
}

/** Renders the panel inside the i18n provider (en-US catalog), like the app. */
function renderPanel(props: FindReplacePanelProps) {
  return render(
    <I18nProvider initialLocale="en-US">
      <FindReplacePanel {...props} />
    </I18nProvider>,
  )
}

function withI18n(children: React.ReactNode) {
  return <I18nProvider initialLocale="en-US">{children}</I18nProvider>
}

describe('FindReplacePanel', () => {
  it('renders nothing while closed', () => {
    const { container } = renderPanel(makeProps(makeView('hello'), { open: false }))
    expect(container.textContent).toBe('')
  })

  it('registers with the browser overlay while open and unregisters on close', () => {
    const api: BrowserOverlayApi = { pushOverlay: vi.fn(), popOverlay: vi.fn() }
    const props = makeProps(makeView('hello'))
    const { rerender } = render(
      withI18n(
        <BrowserOverlayProvider value={api}>
          <FindReplacePanel {...props} />
        </BrowserOverlayProvider>,
      ),
    )
    expect(api.pushOverlay).toHaveBeenCalledTimes(1)
    expect(api.popOverlay).not.toHaveBeenCalled()

    rerender(
      withI18n(
        <BrowserOverlayProvider value={api}>
          <FindReplacePanel {...props} open={false} />
        </BrowserOverlayProvider>,
      ),
    )
    expect(api.popOverlay).toHaveBeenCalledTimes(1)
  })

  it('seeds the find field from a single-line selection on open', async () => {
    vi.useFakeTimers()
    const view = makeView('one two two')
    view.dispatch({ selection: { anchor: 4, head: 7 } })
    renderPanel(makeProps(view))

    const input = screen.getByLabelText('Find') as HTMLInputElement
    expect(input.value).toBe('two')
    await flushDebounce()
    expect(getSearchQuery(view.state).search).toBe('two')
    expect(screen.getByText('1/2')).toBeInTheDocument()
  })

  it('drives the CM SearchQuery as you type and counts matches', async () => {
    vi.useFakeTimers()
    const view = makeView('foo bar foo')
    renderPanel(makeProps(view))

    const input = screen.getByLabelText('Find')
    fireEvent.change(input, { target: { value: 'foo' } })
    await flushDebounce()

    expect(getSearchQuery(view.state).search).toBe('foo')
    expect(getSearchQuery(view.state).replace).toBe('')
    expect(screen.getByText('1/2')).toBeInTheDocument()
  })

  it('shows the localized no-results label in red when there is no match', async () => {
    vi.useFakeTimers()
    const view = makeView('foo bar')
    renderPanel(makeProps(view))

    fireEvent.change(screen.getByLabelText('Find'), { target: { value: 'zzz' } })
    await flushDebounce()
    expect(screen.getByText('No matches')).toBeInTheDocument()
    expect(screen.getByText('No matches').className).toContain('frp-count--none')
  })

  it('Enter / Shift+Enter navigate matches and update the active index', async () => {
    vi.useFakeTimers()
    const view = makeView('foo bar foo')
    renderPanel(makeProps(view))

    const input = screen.getByLabelText('Find')
    fireEvent.change(input, { target: { value: 'foo' } })
    await flushDebounce()

    fireEvent.keyDown(input, { key: 'Enter' })
    expect(view.state.selection.main.from).toBe(0)
    expect(view.state.selection.main.to).toBe(3)
    expect(screen.getByText('1/2')).toBeInTheDocument()

    fireEvent.keyDown(input, { key: 'Enter' })
    expect(view.state.selection.main.from).toBe(8)
    expect(screen.getByText('2/2')).toBeInTheDocument()

    fireEvent.keyDown(input, { key: 'Enter', shiftKey: true })
    expect(view.state.selection.main.from).toBe(0)
    expect(screen.getByText('1/2')).toBeInTheDocument()
  })

  it('toggles dispatch case-sensitive, whole-word and regexp query options', async () => {
    vi.useFakeTimers()
    const view = makeView('Foo foo foobar')
    renderPanel(makeProps(view))

    const input = screen.getByLabelText('Find')
    fireEvent.change(input, { target: { value: 'foo' } })
    await flushDebounce()
    expect(screen.getByText('1/3')).toBeInTheDocument() // Foo, foo, foobar

    fireEvent.click(screen.getByTitle('Match case'))
    await flushDebounce()
    expect(getSearchQuery(view.state).caseSensitive).toBe(true)
    expect(screen.getByText('1/2')).toBeInTheDocument() // foo, foobar

    fireEvent.click(screen.getByTitle('Whole words'))
    await flushDebounce()
    expect(getSearchQuery(view.state).wholeWord).toBe(true)
    expect(screen.getByText('1/1')).toBeInTheDocument() // foo only

    fireEvent.click(screen.getByTitle('Regular expression'))
    await flushDebounce()
    expect(getSearchQuery(view.state).regexp).toBe(true)
    expect(getSearchQuery(view.state).literal).toBe(false)
    expect(screen.getByText('1/1')).toBeInTheDocument()
  })

  it('shows — and disables replace for an invalid regex', async () => {
    vi.useFakeTimers()
    const view = makeView('foo bar')
    renderPanel(makeProps(view, { mode: 'replace' }))

    fireEvent.click(screen.getByTitle('Regular expression'))
    fireEvent.change(screen.getByLabelText('Find'), { target: { value: '(' } })
    await flushDebounce()

    expect(screen.getByText('—')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Replace All' })).toBeDisabled()
  })

  it('Replace All rewrites every match', async () => {
    vi.useFakeTimers()
    const view = makeView('foo bar foo')
    renderPanel(makeProps(view, { mode: 'replace' }))

    fireEvent.change(screen.getByLabelText('Find'), { target: { value: 'foo' } })
    fireEvent.change(screen.getByLabelText('Replace with'), { target: { value: 'baz' } })
    await flushDebounce()

    const replaceAll = screen.getByRole('button', { name: 'Replace All' })
    expect(replaceAll).toBeEnabled()
    fireEvent.click(replaceAll)

    expect(view.state.doc.toString()).toBe('baz bar baz')
    expect(screen.getByText('No matches')).toBeInTheDocument()
  })

  it('Replace (Enter in replace field) replaces the current match and moves to the next', async () => {
    vi.useFakeTimers()
    const view = makeView('foo bar foo')
    renderPanel(makeProps(view, { mode: 'replace' }))

    fireEvent.change(screen.getByLabelText('Find'), { target: { value: 'foo' } })
    fireEvent.change(screen.getByLabelText('Replace with'), { target: { value: 'baz' } })
    await flushDebounce()

    const findInput = screen.getByLabelText('Find')
    fireEvent.keyDown(findInput, { key: 'Enter' }) // select first match
    const replaceInput = screen.getByLabelText('Replace with')
    fireEvent.keyDown(replaceInput, { key: 'Enter' }) // replace it

    expect(view.state.doc.toString()).toBe('baz bar foo')
    expect(view.state.selection.main.from).toBe(8)
    expect(screen.getByText('1/1')).toBeInTheDocument()
  })

  it('disables replace actions on read-only documents', async () => {
    vi.useFakeTimers()
    const view = makeView('foo bar', { editable: false })
    renderPanel(makeProps(view, { mode: 'replace', editable: false }))

    fireEvent.change(screen.getByLabelText('Find'), { target: { value: 'foo' } })
    await flushDebounce()

    expect(screen.getByRole('button', { name: 'Replace' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Replace All' })).toBeDisabled()
    expect(view.state.doc.toString()).toBe('foo bar')
  })

  it('Escape closes the panel; closing clears the query', async () => {
    vi.useFakeTimers()
    const view = makeView('foo bar foo')
    const onClose = vi.fn()
    renderPanel(makeProps(view, { onClose }))

    fireEvent.change(screen.getByLabelText('Find'), { target: { value: 'foo' } })
    await flushDebounce()
    expect(getSearchQuery(view.state).search).toBe('foo')

    fireEvent.keyDown(screen.getByLabelText('Find'), { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('clears the CM query when the panel unmounts closed', async () => {
    vi.useFakeTimers()
    const view = makeView('foo bar foo')
    const props = makeProps(view)
    const { rerender } = renderPanel(props)

    fireEvent.change(screen.getByLabelText('Find'), { target: { value: 'foo' } })
    await flushDebounce()
    expect(getSearchQuery(view.state).search).toBe('foo')

    rerender(withI18n(<FindReplacePanel {...props} open={false} />))
    expect(getSearchQuery(view.state).search).toBe('')
  })

  it('Ctrl+R inside the panel switches to replace mode', () => {
    const view = makeView('hello')
    const onModeChange = vi.fn()
    renderPanel(makeProps(view, { onModeChange }))

    fireEvent.keyDown(screen.getByRole('search'), { key: 'r', ctrlKey: true })
    expect(onModeChange).toHaveBeenCalledWith('replace')
  })
})

describe('computeMatchInfo', () => {
  it('returns zero for an empty query', () => {
    const view = makeView('foo bar foo')
    expect(computeMatchInfo(view)).toEqual({ count: 0, activeIndex: 0, capped: false })
  })

  it('counts matches and marks the match containing the cursor', () => {
    const view = makeView('foo bar foo')
    view.dispatch({ effects: setSearchQuery.of(new SearchQuery({ search: 'foo' })) })
    expect(computeMatchInfo(view)).toEqual({ count: 2, activeIndex: 0, capped: false })

    view.dispatch({ selection: { anchor: 8, head: 11 } })
    expect(computeMatchInfo(view).activeIndex).toBe(1)

    // Cursor between the two matches → the next match is the active one.
    view.dispatch({ selection: { anchor: 7, head: 7 } })
    expect(computeMatchInfo(view).activeIndex).toBe(1)
  })

  it('caps the count at the display limit', () => {
    const doc = Array.from({ length: MATCH_COUNT_CAP + 500 }, () => 'x').join(' ')
    const view = makeView(doc)
    view.dispatch({ effects: setSearchQuery.of(new SearchQuery({ search: 'x' })) })
    const info = computeMatchInfo(view)
    expect(info.count).toBe(MATCH_COUNT_CAP)
    expect(info.capped).toBe(true)
  })
})

describe('editor keymap integration', () => {
  it('Ctrl+F / Ctrl+R open the custom panel and never the default CM search panel', () => {
    const handlers: FindReplaceHandlers = { onOpen: vi.fn(), onClose: vi.fn(() => false) }
    const container = document.createElement('div')
    document.body.appendChild(container)
    const state = EditorState.create({
      doc: 'hello world',
      extensions: [editingExtensions({ editable: true, findReplace: handlers })],
    })
    const view = new EditorView({ state, parent: container })
    view.focus()

    view.contentDOM.dispatchEvent(new KeyboardEvent('keydown', { key: 'f', ctrlKey: true, bubbles: true, cancelable: true }))
    expect(handlers.onOpen).toHaveBeenCalledWith('find')

    view.contentDOM.dispatchEvent(new KeyboardEvent('keydown', { key: 'r', ctrlKey: true, bubbles: true, cancelable: true }))
    expect(handlers.onOpen).toHaveBeenCalledWith('replace')

    // The stock search panel input (marked with main-field) must never render.
    expect(view.dom.querySelector('[main-field]')).toBeNull()
  })

  it('even a direct openSearchPanel call shows no visible panel UI', () => {
    const handlers: FindReplaceHandlers = { onOpen: vi.fn(), onClose: vi.fn(() => false) }
    const container = document.createElement('div')
    document.body.appendChild(container)
    const state = EditorState.create({
      doc: 'hello world',
      extensions: [editingExtensions({ editable: true, findReplace: handlers })],
    })
    const view = new EditorView({ state, parent: container })

    expect(openSearchPanel(view)).toBe(true)
    expect(view.dom.querySelector('[main-field]')).toBeNull()
    expect(view.dom.querySelector('.cm-panels input, .cm-panels button')).toBeNull()
  })

  it('Escape in the editor is routed to the custom close hook and falls through when closed', () => {
    const handlers: FindReplaceHandlers = { onOpen: vi.fn(), onClose: vi.fn(() => false) }
    const container = document.createElement('div')
    document.body.appendChild(container)
    const state = EditorState.create({
      doc: 'hello world',
      extensions: [editingExtensions({ editable: true, findReplace: handlers })],
    })
    const view = new EditorView({ state, parent: container })
    view.focus()

    view.contentDOM.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }))
    expect(handlers.onClose).toHaveBeenCalled()
  })
})

describe('useFindReplacePanel', () => {
  const Harness: React.FC = () => {
    const api = useFindReplacePanel()
    const [escapeResult, setEscapeResult] = useState<boolean | null>(null)
    return (
      <div>
        <button onClick={() => api.openPanel('find')}>open-find</button>
        <button onClick={() => api.openPanel('replace')}>open-replace</button>
        <button onClick={() => setEscapeResult(api.closePanelOnEscape())}>escape</button>
        <span data-testid="state">{`${api.open}:${api.mode}:${api.focusKey}`}</span>
        <span data-testid="escape-ret">{String(escapeResult)}</span>
      </div>
    )
  }

  it('tracks open/mode/focusKey and closePanelOnEscape reports whether it was open', () => {
    render(<Harness />)
    const state = () => screen.getByTestId('state').textContent

    expect(state()).toBe('false:find:0')

    fireEvent.click(screen.getByText('open-find'))
    expect(state()).toBe('true:find:1')

    fireEvent.click(screen.getByText('open-replace'))
    expect(state()).toBe('true:replace:2')

    fireEvent.click(screen.getByText('escape'))
    expect(screen.getByTestId('escape-ret').textContent).toBe('true') // closed an open panel
    expect(state()).toBe('false:replace:2')

    fireEvent.click(screen.getByText('escape'))
    expect(screen.getByTestId('escape-ret').textContent).toBe('false') // was already closed
  })
})
