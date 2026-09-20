import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { WailsRawTransport, type WailsRawTransportOptions } from './wails-raw-transport'

const invokes: string[] = []
const listeners: Record<string, Array<(event: unknown) => void>> = {}

vi.mock('@wailsio/runtime', () => ({
  System: {
    invoke: vi.fn((message: string) => {
      invokes.push(message)
    }),
  },
  Events: {
    On: vi.fn((name: string, handler: (event: unknown) => void) => {
      if (!listeners[name]) listeners[name] = []
      listeners[name].push(handler)
      return () => {
        if (listeners[name]) {
          listeners[name] = listeners[name].filter((h) => h !== handler)
        }
      }
    }),
  },
}))

function emitEvent(name: string, data?: unknown): void {
  const payload = data !== undefined ? { data } : undefined
  const event = payload ?? data
  for (const handler of listeners[name] ?? []) {
    handler(event)
  }
}

function bytesToBase64(data: Uint8Array): string {
  let binary = ''
  for (let offset = 0; offset < data.length; offset += 0x8000) {
    binary += String.fromCharCode(...data.subarray(offset, offset + 0x8000))
  }
  return btoa(binary)
}

function makeTransport(options?: Partial<WailsRawTransportOptions>): WailsRawTransport {
  return new WailsRawTransport({
    url: 'wails://ignored',
    lazyConnect: true,
    getAuthToken: () => null,
    ...options,
  })
}

describe('WailsRawTransport', () => {
  let transports: WailsRawTransport[] = []

  beforeEach(() => {
    invokes.length = 0
    for (const key of Object.keys(listeners)) {
      delete listeners[key]
    }
    transports = []
  })

  afterEach(() => {
    for (const transport of transports) {
      transport.close()
    }
    vi.clearAllMocks()
  })

  it('sends connect envelope on connect', () => {
    const transport = makeTransport()
    transports.push(transport)
    transport.connect()
    emitEvent('sporemind:raw:open')

    expect(invokes.length).toBeGreaterThanOrEqual(1)
    const envelope = JSON.parse(invokes[0]!)
    expect(envelope.type).toBe('connect')
  })

  it('passes token through connect envelope when available', () => {
    const transport = makeTransport({ getAuthToken: () => 'token-123' })
    transports.push(transport)
    transport.connect()
    emitEvent('sporemind:raw:open')

    const connectEnvelope = JSON.parse(invokes[0]!)
    expect(connectEnvelope.type).toBe('connect')
    expect(connectEnvelope.token).toBe('token-123')
  })

  it('becomes ready after open event', async () => {
    const transport = makeTransport()
    transports.push(transport)
    const ready = transport.waitUntilReady()
    transport.connect()

    emitEvent('sporemind:raw:open')
    await expect(ready).resolves.toBeUndefined()
  })

  it('sends frame envelope with base64 data', () => {
    const transport = makeTransport()
    transports.push(transport)
    transport.connect()
    emitEvent('sporemind:raw:open')

    invokes.length = 0
    const bytes = new Uint8Array([1, 2, 3, 4])
    ;(transport as any).connection.send(bytes)

    expect(invokes.length).toBeGreaterThanOrEqual(1)
    const envelope = JSON.parse(invokes[0]!)
    expect(envelope.type).toBe('frame')
    expect(envelope.data).toBe(bytesToBase64(bytes))
  })

  it('receives frame event data without closing the connection', () => {
    const transport = makeTransport()
    transports.push(transport)
    transport.connect()
    emitEvent('sporemind:raw:open')

    const bytes = new Uint8Array([5, 6, 7, 8])
    emitEvent('sporemind:raw:frame', { data: bytesToBase64(bytes) })

    expect((transport as any).connection.state).toBe('open')
  })

  it('handles raw event payload (not wrapped in data)', () => {
    const transport = makeTransport()
    transports.push(transport)
    transport.connect()
    emitEvent('sporemind:raw:open')

    const bytes = new Uint8Array([9, 10])
    emitEvent('sporemind:raw:frame', bytesToBase64(bytes))

    expect((transport as any).connection.state).toBe('open')
  })

  it('unpacks a batched frames[] event and delivers each frame in order', () => {
    const transport = makeTransport()
    transports.push(transport)
    transport.connect()
    emitEvent('sporemind:raw:open')

    const received: Uint8Array[] = []
    ;(transport as any).connection.onmessage = (data: Uint8Array) => received.push(data)

    // Go writeLoop emits a single event carrying N base64 frames in array order.
    const frames = [
      new Uint8Array([1, 2]),
      new Uint8Array([3, 4, 5]),
      new Uint8Array([6]),
    ].map((b) => ({ data: bytesToBase64(b) }))
    emitEvent('sporemind:raw:frame', { frames })

    expect(received.length).toBe(3)
    expect(Array.from(received[0]!)).toEqual([1, 2])
    expect(Array.from(received[1]!)).toEqual([3, 4, 5])
    expect(Array.from(received[2]!)).toEqual([6])
    expect((transport as any).connection.state).toBe('open')
  })

  it('closes cleanly and sends close envelope', () => {
    const transport = makeTransport()
    transports.push(transport)
    transport.connect()
    emitEvent('sporemind:raw:open')

    invokes.length = 0
    transport.close()

    expect((transport as any).connection.state).toBe('closed')
    const last = invokes[invokes.length - 1]
    const envelope = JSON.parse(last!)
    expect(envelope.type).toBe('close')
  })

  it('emits error and closes on backend error event', () => {
    const transport = makeTransport({ onError: vi.fn() })
    transports.push(transport)
    transport.connect()
    emitEvent('sporemind:raw:open')

    emitEvent('sporemind:raw:error', 'backend transport error')

    expect((transport as any).connection.state).toBe('closed')
  })

  it('logs the backend close reason (force-close diagnostics) and closes', () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    try {
      const transport = makeTransport()
      transports.push(transport)
      transport.connect()
      emitEvent('sporemind:raw:open')

      emitEvent('sporemind:raw:close', 'overflow budget 33554432 bytes exceeded after 640 queued frames')

      expect((transport as any).connection.state).toBe('closed')
      expect(warnSpy).toHaveBeenCalledWith(expect.stringContaining('overflow budget 33554432'))
    } finally {
      warnSpy.mockRestore()
    }
  })

  it('falls back to a generic reason when the close event carries no data', () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    try {
      const transport = makeTransport()
      transports.push(transport)
      transport.connect()
      emitEvent('sporemind:raw:open')

      emitEvent('sporemind:raw:close')

      expect((transport as any).connection.state).toBe('closed')
      expect(warnSpy).toHaveBeenCalledWith(expect.stringContaining('backend closed transport'))
    } finally {
      warnSpy.mockRestore()
    }
  })

  it('uses wails-raw transport tag', () => {
    const transport = makeTransport()
    transports.push(transport)
    expect((transport as any).transportTag).toBe('wails-raw')
  })
})
