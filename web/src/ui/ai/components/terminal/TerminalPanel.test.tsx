import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { I18nProvider } from '../../../../i18n'

// Replace the tab registry with lightweight probe components so the test can
// observe mount/unmount lifecycle without pulling in xterm or backend clients.
const mountLog: string[] = []
vi.mock('./tabRegistry', async () => {
  const React = await import('react')
  const makeStub = (type: string) => {
    const Stub = () => {
      React.useEffect(() => {
        mountLog.push(`mount:${type}`)
        return () => { mountLog.push(`unmount:${type}`) }
      }, [])
      return React.createElement('div', { 'data-testid': `tab-body-${type}` })
    }
    Stub.displayName = `Stub(${type})`
    return Stub
  }
  return {
    terminalTabRegistry: {
      shell: { labelKey: 'terminalPanel.tab.shell' as const, icon: null, component: makeStub('shell'), backendCapabilities: [] },
      debug: { labelKey: 'terminalPanel.tab.debug' as const, icon: null, component: makeStub('debug'), backendCapabilities: [] },
    },
    terminalTabTypeOrder: ['shell', 'debug'],
  }
})
const { TerminalPanel } = await import('./TerminalPanel')

function renderPanel(props: Partial<Parameters<typeof TerminalPanel>[0]> = {}) {
  return render(
    <I18nProvider initialLocale="en-US">
      <TerminalPanel
        tabs={[{ id: 't1', type: 'shell' }, { id: 't2', type: 'debug' }]}
        activeTabId="t1"
        open
        height={240}
        isMobile={false}
        onToggle={vi.fn()}
        onSelectTab={vi.fn()}
        onCloseTab={vi.fn()}
        onAddTab={vi.fn()}
        onResize={vi.fn()}
        {...props}
      />
    </I18nProvider>,
  )
}

describe('TerminalPanel keepAlive', () => {
  beforeEach(() => { mountLog.length = 0 })

  it('collapsing the panel hides it without unmounting tab bodies', () => {
    const { rerender } = renderPanel()
    expect(mountLog).toEqual(['mount:shell', 'mount:debug'])

    mountLog.length = 0
    rerender(
      <I18nProvider initialLocale="en-US">
        <TerminalPanel
          tabs={[{ id: 't1', type: 'shell' }, { id: 't2', type: 'debug' }]}
          activeTabId="t1"
          open={false}
          height={240}
          isMobile={false}
          onToggle={vi.fn()}
          onSelectTab={vi.fn()}
          onCloseTab={vi.fn()}
          onAddTab={vi.fn()}
          onResize={vi.fn()}
        />
      </I18nProvider>,
    )
    expect(mountLog).toEqual([])
  })

  it('switching tabs keeps the previous tab mounted', () => {
    const { rerender } = renderPanel({ activeTabId: 't1' })
    expect(screen.getByTestId('tab-body-shell')).toBeTruthy()
    expect(screen.getByTestId('tab-body-debug')).toBeTruthy()

    mountLog.length = 0
    rerender(
      <I18nProvider initialLocale="en-US">
        <TerminalPanel
          tabs={[{ id: 't1', type: 'shell' }, { id: 't2', type: 'debug' }]}
          activeTabId="t2"
          open
          height={240}
          isMobile={false}
          onToggle={vi.fn()}
          onSelectTab={vi.fn()}
          onCloseTab={vi.fn()}
          onAddTab={vi.fn()}
          onResize={vi.fn()}
        />
      </I18nProvider>,
    )
    expect(mountLog).toEqual([])
    expect(screen.getByTestId('tab-body-shell')).toBeTruthy()
    expect(screen.getByTestId('tab-body-debug')).toBeTruthy()
  })

  it('closing a tab is the only path that unmounts its body', () => {
    const { rerender } = renderPanel({ activeTabId: 't1' })
    expect(screen.getByTestId('tab-body-shell')).toBeTruthy()

    rerender(
      <I18nProvider initialLocale="en-US">
        <TerminalPanel
          tabs={[{ id: 't2', type: 'debug' }]}
          activeTabId="t2"
          open
          height={240}
          isMobile={false}
          onToggle={vi.fn()}
          onSelectTab={vi.fn()}
          onCloseTab={vi.fn()}
          onAddTab={vi.fn()}
          onResize={vi.fn()}
        />
      </I18nProvider>,
    )
    expect(screen.queryByTestId('tab-body-shell')).toBeNull()
    expect(screen.getByTestId('tab-body-debug')).toBeTruthy()
  })
})
