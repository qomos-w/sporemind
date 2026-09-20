import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { setConsoleLogSink, getConsoleLogs } = vi.hoisted(() => ({
  setConsoleLogSink: vi.fn(),
  getConsoleLogs: vi.fn((): unknown[] => []),
}))

vi.mock('@qomos/sporemind-shell', () => ({ setConsoleLogSink, getConsoleLogs }))
vi.mock('../bindings/github.com/qomos-w/sporemind/pkg/desktop/app', () => ({
  WriteConsoleLog: vi.fn(() => Promise.resolve()),
}))

import { WriteConsoleLog } from '../bindings/github.com/qomos-w/sporemind/pkg/desktop/app'
import { installConsoleLogPersist } from './console-log-persist'
import { recomputeRuntime } from './runtime'

const writeConsoleLog = WriteConsoleLog as ReturnType<typeof vi.fn>

beforeEach(() => {
  vi.clearAllMocks()
  writeConsoleLog.mockImplementation(() => Promise.resolve())
  getConsoleLogs.mockReturnValue([])
  delete (window as any).chrome
  recomputeRuntime()
})

afterEach(() => {
  vi.useRealTimers()
  delete (window as any).chrome
  recomputeRuntime()
})

function enableWails() {
  ;(window as any).chrome = { webview: { postMessage: vi.fn() } }
  recomputeRuntime()
}

describe('installConsoleLogPersist', () => {
  it('does not register a sink outside Wails', () => {
    installConsoleLogPersist()
    expect(setConsoleLogSink).not.toHaveBeenCalled()
  })

  it('uses the Wails binding for every registered entry', () => {
    enableWails()
    installConsoleLogPersist()
    const sink = setConsoleLogSink.mock.calls[0]![0]

    sink({
      level: 'warn',
      message: 'desktop console message',
      location: 'App.tsx:42',
      time: 123,
    })

    expect(writeConsoleLog).toHaveBeenCalledWith('warn', 'desktop console message', 'App.tsx:42', 123)
  })

  it('filters debug entries from the backend sink', () => {
    enableWails()
    installConsoleLogPersist()
    const sink = setConsoleLogSink.mock.calls[0]![0]

    sink({ level: 'debug', message: '[step.reducer] block.delta', location: '', time: 456 })

    expect(writeConsoleLog).not.toHaveBeenCalled()
  })

  it('truncates oversized messages before the Wails IPC round-trip', () => {
    enableWails()
    installConsoleLogPersist()
    const sink = setConsoleLogSink.mock.calls[0]![0]

    sink({ level: 'info', message: 'x'.repeat(5000), location: '', time: 789 })

    const shipped = writeConsoleLog.mock.calls[0]![1] as string
    expect(shipped.length).toBeLessThan(4200)
    expect(shipped.endsWith('...(truncated)')).toBe(true)
  })

  it('emits a heartbeat every 60s so a dead pipeline is observable', () => {
    vi.useFakeTimers()
    enableWails()
    installConsoleLogPersist()

    vi.advanceTimersByTime(60_000)
    expect(writeConsoleLog).toHaveBeenCalledTimes(1)
    const [level, message, location] = writeConsoleLog.mock.calls[0] as [string, string, string]
    expect(level).toBe('info')
    expect(message).toContain('[console-patch] heartbeat')
    expect(message).toContain('buffered=0')
    expect(message).toContain('failures=0')
    expect(location).toBe('console-log-persist')

    vi.advanceTimersByTime(60_000)
    expect(writeConsoleLog).toHaveBeenCalledTimes(2)
  })

  it('retries a failed write once and reports the failure in the next heartbeat', async () => {
    vi.useFakeTimers()
    enableWails()
    installConsoleLogPersist()

    writeConsoleLog.mockImplementation(() => Promise.reject(new Error('ipc wedged')))
    const sink = setConsoleLogSink.mock.calls[0]![0]
    sink({ level: 'warn', message: 'entry', location: '', time: 1 })
    expect(writeConsoleLog).toHaveBeenCalledTimes(1)

    await vi.advanceTimersByTimeAsync(600)
    // Initial attempt + one retry.
    expect(writeConsoleLog).toHaveBeenCalledTimes(2)

    writeConsoleLog.mockImplementation(() => Promise.resolve())
    await vi.advanceTimersByTimeAsync(60_000)
    const heartbeat = writeConsoleLog.mock.calls.at(-1)![1] as string
    expect(heartbeat).toContain('failures=1')
    expect(heartbeat).toContain('last_error=ipc wedged')
  })
})
