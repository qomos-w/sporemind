import '@testing-library/jest-dom/vitest'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { ReferencesPopup, type ReferencesPopupProps } from './ReferencesPopup'

const locations = [
  { uri: 'file:///repo/pkg/actor/foo.go', range: { start: { line: 0, character: 0 }, end: { line: 0, character: 5 } } },
  { uri: 'file:///repo/pkg/actor/bar.go', range: { start: { line: 9, character: 2 }, end: { line: 9, character: 7 } } },
  { uri: 'file:///repo/pkg/other/baz.go', range: { start: { line: 3, character: 1 }, end: { line: 3, character: 6 } } },
]

const queryPos = { uri: 'file:///repo/pkg/actor/foo.go', line: 0, character: 3 }

function makeProps(overrides: Partial<ReferencesPopupProps> = {}): ReferencesPopupProps {
  return {
    locations,
    anchor: { x: 100, y: 200 },
    queryPos,
    projectRootPath: '/repo',
    onClose: vi.fn(),
    onJump: vi.fn(),
    ...overrides,
  }
}

describe('ReferencesPopup', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
  })

  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('renders relativized, truncated paths with line numbers', () => {
    render(<ReferencesPopup {...makeProps()} />)
    expect(screen.getByText('pkg/actor/foo.go')).toBeInTheDocument()
    expect(screen.getByText(':1')).toBeInTheDocument()
    expect(screen.getByText('pkg/actor/bar.go')).toBeInTheDocument()
    expect(screen.getByText(':10')).toBeInTheDocument()
    expect(screen.getByText('pkg/other/baz.go')).toBeInTheDocument()
    expect(screen.getByText(':4')).toBeInTheDocument()
  })

  it('renders shadcn scope wrapper for theme adaptation', () => {
    const { container } = render(<ReferencesPopup {...makeProps()} />)
    const popup = container.querySelector('.shadcn-scope')
    expect(popup).not.toBeNull()
    expect(popup!.className).toContain('bg-popover')
  })

  it('renders the code line preview with syntax-colored spans', async () => {
    // Mock mirrors filesystem.read's 1-based contract: StartLine echoes the Offset.
    const fetchLines = vi.fn(async (_path: string, offset: number) =>
      ({ Content: 'func NewGreeter() *Greeter {\n', StartLine: offset }))
    render(<ReferencesPopup {...makeProps({ fetchLines })} />)
    await waitFor(() => {
      expect(fetchLines).toHaveBeenCalledWith('/repo/pkg/actor/foo.go', 1, 1)
      expect(fetchLines).toHaveBeenCalledWith('/repo/pkg/actor/bar.go', 10, 1)
      expect(fetchLines).toHaveBeenCalledWith('/repo/pkg/other/baz.go', 4, 1)
    })
    await waitFor(() => {
      // All three rows share the mock line content, so expect multiple matches.
      expect(screen.getAllByTitle('func NewGreeter() *Greeter {').length).toBeGreaterThan(0)
    })
    const kw = screen.getAllByTitle('func NewGreeter() *Greeter {')[0]!.querySelector('span[style]')
    expect(kw).not.toBeNull()
  })

  it('truncates overlong code lines', async () => {
    const longLine = 'x := ' + 'a'.repeat(300)
    const fetchLines = vi.fn(async (_path: string, offset: number) => ({ Content: longLine, StartLine: offset }))
    render(<ReferencesPopup {...makeProps({ fetchLines })} />)
    await waitFor(() => {
      const els = screen.getAllByTitle(longLine)
      expect(els[0]!.textContent!.length).toBeLessThanOrEqual(161)
    })
  })

  it('hides scope tabs when no workspace fetcher is provided', () => {
    render(<ReferencesPopup {...makeProps()} />)
    expect(screen.queryByText('Workspace')).not.toBeInTheDocument()
  })

  it('switches to workspace scope and merges fetched references', async () => {
    const workspaceLocations = [
      { uri: 'file:///other/gospore/actor/host.go', range: { start: { line: 4, character: 0 }, end: { line: 4, character: 3 } } },
      // Duplicate of a project hit — must be deduped.
      { uri: 'file:///repo/pkg/actor/bar.go', range: { start: { line: 9, character: 2 }, end: { line: 9, character: 7 } } },
    ]
    const onFetchWorkspaceRefs = vi.fn().mockResolvedValue(workspaceLocations)
    render(<ReferencesPopup {...makeProps({ onFetchWorkspaceRefs })} />)
    fireEvent.click(screen.getByText('Workspace'))
    expect(onFetchWorkspaceRefs).toHaveBeenCalledWith(queryPos)
    await waitFor(() => {
      expect(screen.getByText(/host\.go/)).toBeInTheDocument()
    })
    // Dedupe: bar.go:10 appears once.
    expect(screen.getAllByText(':10')).toHaveLength(1)
  })

  it('shows workspace error state on fetch failure', async () => {
    const onFetchWorkspaceRefs = vi.fn().mockRejectedValue(new Error('gopls down'))
    render(<ReferencesPopup {...makeProps({ onFetchWorkspaceRefs })} />)
    fireEvent.click(screen.getByText('Workspace'))
    await waitFor(() => {
      expect(screen.getByText(/Workspace search failed: gopls down/)).toBeInTheDocument()
    })
  })

  it('filters results by path substring', () => {
    render(<ReferencesPopup {...makeProps()} />)
    const input = screen.getByPlaceholderText('Filter references…')
    fireEvent.change(input, { target: { value: 'bar' } })
    expect(screen.getByText('pkg/actor/bar.go')).toBeInTheDocument()
    expect(screen.queryByText('pkg/actor/foo.go')).not.toBeInTheDocument()
    expect(screen.queryByText('pkg/other/baz.go')).not.toBeInTheDocument()
  })

  it('filters results by line number', () => {
    render(<ReferencesPopup {...makeProps()} />)
    const input = screen.getByPlaceholderText('Filter references…')
    fireEvent.change(input, { target: { value: '10' } })
    expect(screen.getByText('pkg/actor/bar.go')).toBeInTheDocument()
    expect(screen.queryByText('pkg/actor/foo.go')).not.toBeInTheDocument()
  })

  it('shows empty state when no results match', () => {
    render(<ReferencesPopup {...makeProps()} />)
    const input = screen.getByPlaceholderText('Filter references…')
    fireEvent.change(input, { target: { value: 'zzz' } })
    expect(screen.getByText('No matches')).toBeInTheDocument()
  })

  it('navigates with ArrowDown/ArrowUp and jumps with Enter', () => {
    const onJump = vi.fn()
    render(<ReferencesPopup {...makeProps({ onJump })} />)
    const input = screen.getByPlaceholderText('Filter references…')
    fireEvent.keyDown(input, { key: 'ArrowDown' })
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(onJump).toHaveBeenCalledWith('/repo/pkg/actor/bar.go', 9)
  })

  it('jumps with Enter on the first item by default', () => {
    const onJump = vi.fn()
    render(<ReferencesPopup {...makeProps({ onJump })} />)
    const input = screen.getByPlaceholderText('Filter references…')
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(onJump).toHaveBeenCalledWith('/repo/pkg/actor/foo.go', 0)
  })

  it('closes on Escape', () => {
    const onClose = vi.fn()
    render(<ReferencesPopup {...makeProps({ onClose })} />)
    const input = screen.getByPlaceholderText('Filter references…')
    fireEvent.keyDown(input, { key: 'Escape' })
    expect(onClose).toHaveBeenCalled()
  })

  it('jumps on item click', () => {
    const onJump = vi.fn()
    render(<ReferencesPopup {...makeProps({ onJump })} />)
    fireEvent.click(screen.getByText('pkg/other/baz.go'))
    expect(onJump).toHaveBeenCalledWith('/repo/pkg/other/baz.go', 3)
  })

  it('closes on click outside', () => {
    const onClose = vi.fn()
    render(<ReferencesPopup {...makeProps({ onClose })} />)
    fireEvent.mouseDown(document.body)
    expect(onClose).toHaveBeenCalled()
  })
})

describe('highlightGoLine', () => {
  it('tokenizes go source with syntax colors', async () => {
    const { highlightGoLine } = await import('./highlightGo')
    const spans = highlightGoLine('func NewThing() string {')
    expect(spans.length).toBeGreaterThan(1)
    expect(spans[0]!.text).toBe('func')
    expect(spans[0]!.color).toBe('var(--syntax-keyword)')
  })

  it('returns plain span for unparseable input', async () => {
    const { highlightGoLine } = await import('./highlightGo')
    const spans = highlightGoLine('')
    expect(spans).toEqual([])
  })
})
