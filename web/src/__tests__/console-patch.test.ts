import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  installConsolePatch,
  getConsoleLogs,
  clearConsoleLogs,
  subscribeConsoleLogs,
  setConsoleLogSink,
} from '@qomos/sporemind-shell'

describe('console-patch', () => {
  beforeEach(() => {
    clearConsoleLogs()
    installConsolePatch()
  })

  afterEach(() => {
    vi.restoreAllMocks()
    delete (window as any).go
  })

  it('does not throw when backend Log is missing', () => {
    expect(() => {
      console.log('no backend')
    }).not.toThrow()
  })

  it('does not throw when window.go is missing', () => {
    delete (window as any).go

    expect(() => {
      console.log('no wails')
    }).not.toThrow()
  })

  it('captures log entry in local buffer', async () => {
    console.warn('buffered warning')

    await new Promise<void>(r => queueMicrotask(() => r()))
    await new Promise(r => setTimeout(r, 10))

    const logs = getConsoleLogs()
    expect(logs.length).toBeGreaterThan(0)
    const entry = logs[logs.length - 1]!
    expect(entry.level).toBe('warn')
    expect(entry.message).toContain('buffered warning')
    expect(typeof entry.time).toBe('number')
  })

  it('notifies subscribers when a new log is captured', async () => {
    const subscriber = vi.fn()
    subscribeConsoleLogs(subscriber)

    console.error('subscriber test')

    await new Promise<void>(r => queueMicrotask(() => r()))
    await new Promise(r => setTimeout(r, 10))

    expect(subscriber).toHaveBeenCalled()
  })

  it('captures all four log levels', async () => {
    console.log('info msg')
    console.warn('warn msg')
    console.error('error msg')
    console.debug('debug msg')

    await new Promise(r => setTimeout(r, 50))

    const logs = getConsoleLogs()
    const levels = logs.slice(-4).map(l => l.level)
    expect(levels).toContain('info')
    expect(levels).toContain('warn')
    expect(levels).toContain('error')
    expect(levels).toContain('debug')
  })

  it('forwards captured entries to the registered persist sink', async () => {
    const sink = vi.fn()
    setConsoleLogSink(sink)
    try {
      console.error('sink test')

      await new Promise<void>(r => queueMicrotask(() => r()))
      await new Promise(r => setTimeout(r, 10))

      expect(sink).toHaveBeenCalled()
      const entry = sink.mock.calls[sink.mock.calls.length - 1]![0]
      expect(entry.level).toBe('error')
      expect(entry.message).toContain('sink test')
      expect(typeof entry.time).toBe('number')
    } finally {
      setConsoleLogSink(null)
    }
  })
})
