import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import React from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import {
  GlobalFindReplaceOverlay,
  escapeRegExp,
  buildSearchPattern,
  findLineSpans,
  applyRegexToLine,
  applyReplacementToContent,
} from './GlobalFindReplaceOverlay'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const nativeInputValueSetter = Object.getOwnPropertyDescriptor(
  HTMLInputElement.prototype, 'value',
)!.set!

const setInputValue = (input: HTMLInputElement, value: string) => {
  nativeInputValueSetter.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

const click = (el: Element) => {
  el.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }))
}

const pressKey = (target: Window | Element, key: string) => {
  target.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true }))
}

const grepMock = vi.hoisted(() => vi.fn())
const readBase64Mock = vi.hoisted(() => vi.fn())
const writeMock = vi.hoisted(() => vi.fn())
const tMock = vi.hoisted(() => vi.fn((key: string, params?: Record<string, string | number>) => {
  if (!params) return key
  let out = key
  for (const [name, value] of Object.entries(params)) {
    out = out.replace(new RegExp(`\\{${name}\\}`, 'g'), String(value))
  }
  return out
}))

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: tMock }),
}))

vi.mock('../../../application/generated-client', () => ({
  client: {},
}))

vi.mock('../../../gen-clients/project/client', () => ({
  grep: grepMock,
  readBase64: readBase64Mock,
  write: writeMock,
}))

vi.mock('../browserOverlay', () => ({
  useBrowserOverlay: () => {},
}))

describe('escapeRegExp', () => {
  it('escapes regex metacharacters', () => {
    expect(escapeRegExp('a.b*c')).toBe('a\\.b\\*c')
    expect(escapeRegExp('[foo](bar)')).toBe('\\[foo\\]\\(bar\\)')
    expect(escapeRegExp('plain')).toBe('plain')
  })
})

describe('buildSearchPattern', () => {
  it('escapes literal patterns for both engines', () => {
    const result = buildSearchPattern('a.b', { regex: false, ignoreCase: false, wholeWord: false })
    expect(result).not.toBeNull()
    expect(result!.jsSource).toBe('a\\.b')
    expect(result!.goPattern).toBe('a\\.b')
    expect(result!.jsFlags).toBe('g')
  })

  it('prefixes (?i) for the Go engine when ignoreCase', () => {
    const result = buildSearchPattern('foo', { regex: false, ignoreCase: true, wholeWord: false })
    expect(result!.goPattern).toBe('(?i)foo')
    expect(result!.jsFlags).toBe('ig')
  })

  it('wraps word boundaries for whole-word mode', () => {
    const result = buildSearchPattern('foo', { regex: false, ignoreCase: false, wholeWord: true })
    expect(result!.jsSource).toBe('(?:\\bfoo\\b)')
  })

  it('returns null for invalid regex', () => {
    expect(buildSearchPattern('[unclosed', { regex: true, ignoreCase: false, wholeWord: false })).toBeNull()
  })

  it('returns null for empty query', () => {
    expect(buildSearchPattern('', { regex: false, ignoreCase: false, wholeWord: false })).toBeNull()
  })
})

describe('findLineSpans', () => {
  it('finds all match spans with correct indices', () => {
    const re = new RegExp('foo', 'g')
    const spans = findLineSpans('a foo b foo', re)
    expect(spans).toEqual([
      { start: 2, end: 5 },
      { start: 8, end: 11 },
    ])
  })

  it('supports capture-group regexes', () => {
    const re = new RegExp('(\\w+)@(\\w+)', 'g')
    const spans = findLineSpans('user@host and admin@box', re)
    expect(spans).toEqual([
      { start: 0, end: 9 },
      { start: 14, end: 23 },
    ])
  })
})

describe('applyRegexToLine', () => {
  it('replaces all occurrences with $-group expansion', () => {
    const re = new RegExp('(\\w+)@(\\w+)', 'g')
    expect(applyRegexToLine('user@host', re, '$2.$1')).toBe('host.user')
  })

  it('supports case-insensitive flags', () => {
    const re = new RegExp('foo', 'ig')
    expect(applyRegexToLine('Foo BAR foo', re, 'x')).toBe('x BAR x')
  })
})

describe('applyReplacementToContent', () => {
  const re = () => new RegExp('old', 'g')

  it('replaces only the requested lines', () => {
    const content = 'line1 old\nline2\nline3 old\n'
    const result = applyReplacementToContent(content, [1], re(), 'new')
    expect(result.output).toBe('line1 new\nline2\nline3 old\n')
    expect(result.replacements).toBe(1)
    expect(result.skippedLines).toBe(0)
  })

  it('preserves CRLF line endings', () => {
    const content = 'line1 old\r\nline2 old\r\n'
    const result = applyReplacementToContent(content, [1, 2], re(), 'new')
    expect(result.output).toBe('line1 new\r\nline2 new\r\n')
    expect(result.replacements).toBe(2)
  })

  it('counts skipped lines when the line no longer matches', () => {
    const content = 'changed\nold\n'
    const result = applyReplacementToContent(content, [1], re(), 'new')
    expect(result.replacements).toBe(0)
    expect(result.skippedLines).toBe(1)
    expect(result.output).toBe(content)
  })

  it('skips out-of-range line numbers', () => {
    const content = 'old\n'
    const result = applyReplacementToContent(content, [99], re(), 'new')
    expect(result.skippedLines).toBe(1)
    expect(result.output).toBe(content)
  })
})

describe('GlobalFindReplaceOverlay', () => {
  let container: HTMLDivElement
  let root: Root
  const onOpenFile = vi.fn()
  const onClose = vi.fn()
  const onModeChange = vi.fn()

  const baseProps = {
    open: true,
    mode: 'find' as const,
    onModeChange,
    onClose,
    projectId: 'proj-1',
    onOpenFile,
  }

  const topbar = () => {
    const el = document.createElement('div')
    el.className = 'ai-shell-topbar'
    el.style.height = '48px'
    document.body.appendChild(el)
    return el
  }

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    topbar()
    grepMock.mockReset()
    readBase64Mock.mockReset()
    writeMock.mockReset()
    onOpenFile.mockReset()
    onClose.mockReset()
    onModeChange.mockReset()
  })

  afterEach(() => {
    act(() => root?.unmount())
    container.remove()
    document.querySelectorAll('.ai-shell-topbar').forEach((el) => el.remove())
    vi.restoreAllMocks()
  })

  const render = async (props: Partial<React.ComponentProps<typeof GlobalFindReplaceOverlay>> = {}) => {
    await act(async () => {
      root = createRoot(container)
      root.render(<GlobalFindReplaceOverlay {...baseProps} {...props} />)
    })
  }

  const typeQuery = async (value: string) => {
    const input = container.querySelector('.gfr-input') as HTMLInputElement
    await act(async () => {
      setInputValue(input, value)
    })
    return input
  }

  const clickSearch = async () => {
    const btn = container.querySelector('.gfr-search-btn') as HTMLButtonElement
    await act(async () => {
      click(btn)
    })
  }

  it('renders search input and toggles when open', async () => {
    await render()
    expect(container.querySelector('.gfr-input')).toBeTruthy()
    expect(container.querySelectorAll('.gfr-toggle').length).toBe(3)
  })

  it('renders nothing when closed', async () => {
    await render({ open: false })
    expect(container.querySelector('.global-find-replace-overlay')).toBeNull()
  })

  it('calls project.grep with escaped literal pattern and renders grouped results', async () => {
    grepMock.mockResolvedValue({
      Matches: [
        { File: 'src/a.go', Line: 3, Content: 'hello world' },
        { File: 'src/a.go', Line: 7, Content: 'say hello again' },
        { File: 'src/b.ts', Line: 1, Content: 'hello there' },
      ],
      NumMatches: 3,
      Truncated: false,
      Output_mode: 'content',
    })
    await render()
    await typeQuery('hello')
    await clickSearch()

    expect(grepMock).toHaveBeenCalledTimes(1)
    const firstCall = grepMock.mock.calls[0]
    expect(firstCall).toBeDefined()
    const [clientArg, reqArg, optsArg] = firstCall as NonNullable<typeof firstCall>
    expect(clientArg).toBeDefined()
    expect(reqArg.Pattern).toBe('hello')
    expect(reqArg.Output_mode).toBe('content')
    expect(optsArg).toEqual({ target: 'proj-1' })

    const fileHeaders = container.querySelectorAll('.gfr-file-header')
    expect(fileHeaders.length).toBe(2)
    const matchRows = container.querySelectorAll('.gfr-match-row')
    expect(matchRows.length).toBe(3)
  })

  it('shows the no-results empty state', async () => {
    grepMock.mockResolvedValue({ Matches: [], NumMatches: 0, Truncated: false })
    await render()
    await typeQuery('zzz-not-found')
    await clickSearch()
    expect(container.querySelector('.gfr-empty')?.textContent).toContain('findReplace.noResults')
  })

  it('shows search errors from the backend', async () => {
    grepMock.mockRejectedValue(new Error('invalid pattern: boom'))
    await render()
    await typeQuery('x')
    await clickSearch()
    expect(container.querySelector('.gfr-status-error')?.textContent).toContain('invalid pattern: boom')
  })

  it('jumps to file+line+column on match row click', async () => {
    grepMock.mockResolvedValue({
      Matches: [{ File: 'src/a.go', Line: 3, Content: 'prefix hello suffix' }],
      NumMatches: 1,
      Truncated: false,
      Output_mode: 'content',
    })
    await render()
    await typeQuery('hello')
    await clickSearch()

    const row = container.querySelector('.gfr-match-row') as HTMLElement
    await act(async () => {
      click(row)
    })
    expect(onOpenFile).toHaveBeenCalledWith('src/a.go', 3, 3, 7, 12)
  })

  it('closes on Escape', async () => {
    await render()
    await act(async () => {
      pressKey(window, 'Escape')
    })
    expect(onClose).toHaveBeenCalled()
  })

  it('replace mode shows replacement input and before/after previews', async () => {
    grepMock.mockResolvedValue({
      Matches: [{ File: 'src/a.go', Line: 3, Content: 'use foo here' }],
      NumMatches: 1,
      Truncated: false,
      Output_mode: 'content',
    })
    await render({ mode: 'replace' })
    const inputs = container.querySelectorAll('.gfr-input')
    expect(inputs.length).toBe(2)
    await act(async () => {
      setInputValue(inputs[0] as HTMLInputElement, 'foo')
      setInputValue(inputs[1] as HTMLInputElement, 'bar')
    })
    await clickSearch()

    const del = container.querySelector('.gfr-preview-del')?.textContent
    const ins = container.querySelector('.gfr-preview-ins')?.textContent
    expect(del).toBe('use foo here')
    expect(ins).toBe('use bar here')
  })

  it('applies replacements via readBase64 + write', async () => {
    const content = 'keep old one\nnothing here\n'
    const b64 = btoa(content)
    grepMock.mockResolvedValue({
      Matches: [{ File: 'src/a.go', Line: 1, Content: 'keep old one' }],
      NumMatches: 1,
      Truncated: false,
      Output_mode: 'content',
    })
    readBase64Mock.mockResolvedValue({ Content: b64 })
    writeMock.mockResolvedValue({})

    await render({ mode: 'replace' })
    const inputs = container.querySelectorAll('.gfr-input')
    await act(async () => {
      setInputValue(inputs[0] as HTMLInputElement, 'old')
      setInputValue(inputs[1] as HTMLInputElement, 'new')
    })
    await clickSearch()

    const replaceBtn = container.querySelector('.gfr-replace-btn') as HTMLButtonElement
    expect(replaceBtn).toBeTruthy()
    await act(async () => {
      click(replaceBtn)
    })

    expect(readBase64Mock).toHaveBeenCalledWith(expect.anything(), { Path: 'src/a.go' }, { target: 'proj-1' })
    expect(writeMock).toHaveBeenCalledWith(
      expect.anything(),
      { Path: 'src/a.go', Content: 'keep new one\nnothing here\n' },
      { target: 'proj-1' },
    )
    expect(container.querySelector('.gfr-footer-info')?.textContent).toContain('findReplace.applySummary')
  })

  it('respects per-file deselection before applying', async () => {
    const content = 'old\n'
    grepMock.mockResolvedValue({
      Matches: [
        { File: 'a.go', Line: 1, Content: 'old' },
        { File: 'b.go', Line: 1, Content: 'old' },
      ],
      NumMatches: 2,
      Truncated: false,
      Output_mode: 'content',
    })
    readBase64Mock.mockResolvedValue({ Content: btoa(content) })
    writeMock.mockResolvedValue({})

    await render({ mode: 'replace' })
    const inputs = container.querySelectorAll('.gfr-input')
    await act(async () => {
      setInputValue(inputs[0] as HTMLInputElement, 'old')
      setInputValue(inputs[1] as HTMLInputElement, 'new')
    })
    await clickSearch()

    // Deselect the first file (a.go) via its header checkbox.
    const checkboxes = container.querySelectorAll('.gfr-file-header input[type="checkbox"]')
    expect(checkboxes.length).toBe(2)
    const firstCheckbox = checkboxes[0]
    expect(firstCheckbox).toBeDefined()
    await act(async () => {
      click(firstCheckbox as Element)
    })

    const replaceBtn = container.querySelector('.gfr-replace-btn') as HTMLButtonElement
    await act(async () => {
      click(replaceBtn)
    })

    expect(writeMock).toHaveBeenCalledTimes(1)
    const writeCall = writeMock.mock.calls[0]
    expect(writeCall).toBeDefined()
    expect((writeCall as NonNullable<typeof writeCall>)[1].Path).toBe('b.go')
  })
})
