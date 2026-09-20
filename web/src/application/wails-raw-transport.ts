/**
 * Wails raw-message IPC transport for sporemind desktop.
 *
 * Uses Wails v3 RawMessageHandler on the backend and @wailsio/runtime
 * System.invoke / Events on the frontend. All frames travel as base64
 * payloads inside small JSON envelopes, preserving the strict FIFO order of
 * Wails' native IPC while bypassing the local WebSocket loopback.
 */

import { System, Events } from '@wailsio/runtime'
import {
  ManagedFrameTransport,
  type FrameConnection,
  type FrameConnectionState,
  type ManagedFrameTransportOptions,
  type ManagedTransport,
  type SchemaRegistryLike,
  type BinaryCodecLike,
} from '@qomos/gospore-client'

/*
 * Temporary instrumentation to verify the root cause of slow frontend refresh
 * (Trace-20260724T030404). Remove once the performance issue is resolved.
 */
let _invokeSeq = 0
function instrumentedInvoke(label: string, payload: string): void {
  const seq = ++_invokeSeq
  const start = performance.now()
  try {
    System.invoke(payload)
  } finally {
    const elapsed = performance.now() - start
    if (elapsed > 100) {
      console.warn(`[WailsRaw] invoke #${seq} ${label} took ${elapsed.toFixed(1)}ms`)
    }
  }
}

/* -------------------------------------------------------------------------- */
/* Base64 helpers (kept dependency-free)                                     */
/* -------------------------------------------------------------------------- */

function bytesToBase64(data: Uint8Array): string {
  let binary = ''
  for (let offset = 0; offset < data.length; offset += 0x8000) {
    binary += String.fromCharCode(...data.subarray(offset, offset + 0x8000))
  }
  return btoa(binary)
}

function base64ToBytes(data: string): Uint8Array {
  const binary = atob(data)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i)
  return bytes
}

/* -------------------------------------------------------------------------- */
/* Wire protocol envelopes                                                    */
/* -------------------------------------------------------------------------- */

type RawEnvelopeType = 'connect' | 'frame' | 'close'

interface RawEnvelope {
  type: RawEnvelopeType
  token?: string
  data?: string
}

interface RawFramePayload {
  data?: string
}

/**
 * Batched frame payload. The Go writeLoop coalesces N base64 frames into a
 * single frames[] event to collapse N main-thread InvokeSync round-trips into
 * one. Frames are processed in array order (intra-batch order); cross-batch
 * order is preserved by the single writer goroutine + FIFO channel.
 */
interface RawBatchPayload {
  frames?: RawFramePayload[]
}

/* -------------------------------------------------------------------------- */
/* Frame connection (IO adapter)                                               */
/* -------------------------------------------------------------------------- */

const DEFAULT_FRAME_EVENT = 'sporemind:raw:frame'
const DEFAULT_CLOSE_EVENT = 'sporemind:raw:close'
const DEFAULT_ERROR_EVENT = 'sporemind:raw:error'
const DEFAULT_OPEN_EVENT = 'sporemind:raw:open'

class WailsRawFrameConnection implements FrameConnection {
  private getToken: () => string | null
  private frameEvent: string
  private closeEvent: string
  private errorEvent: string
  private openEvent: string

  private _state: FrameConnectionState = 'closed'
  private cancelFrame: (() => void) | null = null
  private cancelClose: (() => void) | null = null
  private cancelError: (() => void) | null = null
  private cancelOpen: (() => void) | null = null

  onopen: (() => void) | null = null
  onmessage: ((data: Uint8Array) => void) | null = null
  onclose: ((code: number, reason: string, wasClean: boolean) => void) | null = null
  onerror: ((err: Error) => void) | null = null

  constructor(
    getToken: () => string | null,
    frameEvent = DEFAULT_FRAME_EVENT,
    closeEvent = DEFAULT_CLOSE_EVENT,
    errorEvent = DEFAULT_ERROR_EVENT,
    openEvent = DEFAULT_OPEN_EVENT,
  ) {
    this.getToken = getToken
    this.frameEvent = frameEvent
    this.closeEvent = closeEvent
    this.errorEvent = errorEvent
    this.openEvent = openEvent
  }

  get state(): FrameConnectionState {
    return this._state
  }

  connect(_url: string): void {
    if (this._state !== 'closed') return
    this._state = 'connecting'

    this.cancelFrame = Events.On(this.frameEvent, (event: unknown) => {
      const _t0 = typeof performance !== 'undefined' ? performance.now() : 0
      const payload = (event as { data?: unknown })?.data ?? event
      const body = (typeof payload === 'object' && payload !== null
        ? payload
        : event) as RawBatchPayload | RawFramePayload
      // Batched format: a frames[] array processed in order. Each frame is
      // isolated: one throwing handler must not abort the loop, or the
      // skipped remainder surfaces as a transId gap and kills the session.
      const frames = (body as RawBatchPayload)?.frames
      if (Array.isArray(frames)) {
        for (const f of frames) {
          if (typeof f?.data !== 'string') continue
          try {
            this.onmessage?.(base64ToBytes(f.data))
          } catch (err) {
            console.error('[wails-raw] frame handler threw:', err)
          }
        }
        const _t1 = typeof performance !== 'undefined' ? performance.now() : 0
        if (_t1 - _t0 > 50) {
          console.warn(`[PERF wails.onmessage batch] SLOW: ${(_t1-_t0).toFixed(1)}ms frames=${frames.length}`)
          // [jitter-diag] temporary instrumentation: name the culprit frame.
          const slowFrame = frames.find(f => typeof f?.data === 'string')
          if (slowFrame) {
            const b64 = slowFrame.data as string
            const aligned = b64.slice(0, 2728) // multiple of 4 keeps base64 quad alignment
            try {
              const text = new TextDecoder().decode(base64ToBytes(aligned))
              const label = text.match(/"label"\s*:\s*"([^"]{0,64})"/)?.[1] ?? '?'
              const kind = text.match(/"kind"\s*:\s*"([^"]{0,64})"/)?.[1] ?? '?'
              console.info(`[jitter-diag] slow-frame b64len=${b64.length} label=${label} kind=${kind} head=${text.slice(0, 160).replace(/\s+/g, ' ')}`)
            } catch { /* decode failure of a diagnostic prefix is non-fatal */ }
          }
        }
        return
      }
      // Legacy single-frame format.
      const data = (body as RawFramePayload)?.data
      if (typeof data === 'string') {
        try {
          this.onmessage?.(base64ToBytes(data))
        } catch (err) {
          console.error('[wails-raw] frame handler threw:', err)
        }
      }
    })

    this.cancelClose = Events.On(this.closeEvent, (event: unknown) => {
      const reason = (event as { data?: string })?.data
      const text = typeof reason === 'string' && reason.length > 0 ? reason : 'backend closed transport'
      // Persisted via the console-log pipeline: get_system_logs(Source="console")
      // shows why the backend tore the session down (e.g. overflow budget exceeded).
      console.warn(`[wails-raw] backend closed transport: ${text}`)
      this.finishClose(1006, text, false)
    })

    this.cancelError = Events.On(this.errorEvent, (event: unknown) => {
      const message = (event as { data?: string })?.data ?? 'backend transport error'
      this.onerror?.(new Error(message))
      this.finishClose(1006, message, false)
    })

    this.cancelOpen = Events.On(this.openEvent, () => {
      if (this._state !== 'connecting') return
      this._state = 'open'
      this.onopen?.()
    })

    this.doConnect()
  }

  private doConnect(): void {
    try {
      const envelope: RawEnvelope = {
        type: 'connect',
        token: this.getToken() ?? '',
      }
      instrumentedInvoke('connect', JSON.stringify(envelope))
    } catch (err) {
      this.onerror?.(err instanceof Error ? err : new Error('connect failed'))
      this.finishClose(1006, 'connect failed', false)
    }
  }

  send(bytes: Uint8Array): void {
    if (this._state !== 'open') {
      throw new Error('WailsRawFrameConnection is not open')
    }
    const envelope: RawEnvelope = {
      type: 'frame',
      data: bytesToBase64(bytes),
    }
    try {
      instrumentedInvoke('frame', JSON.stringify(envelope))
    } catch (err) {
      this.onerror?.(err instanceof Error ? err : new Error('backend send failed'))
      this.finishClose(1006, 'send failed', false)
    }
  }

  close(code?: number, _reason?: string): void {
    if (this._state === 'closing' || this._state === 'closed') return
    this._state = 'closing'
    try {
      instrumentedInvoke('close', JSON.stringify({ type: 'close' }))
    } catch {
      // ignore — we are closing anyway
    }
    this.finishClose(code ?? 1000, _reason ?? '', true)
  }

  private finishClose(code: number, reason: string, wasClean: boolean): void {
    if (this._state === 'closed') return
    this._state = 'closed'
    this.cancelFrame?.()
    this.cancelClose?.()
    this.cancelError?.()
    this.cancelOpen?.()
    this.onclose?.(code, reason, wasClean)
  }
}

/* -------------------------------------------------------------------------- */
/* Transport (public API)                                                     */
/* -------------------------------------------------------------------------- */

export interface WailsRawTransportOptions {
  url: string
  getAuthToken?: () => string | null
  getUrl?: () => string
  onAuthFailure?: () => void
  lazyConnect?: boolean
  reconnectBaseMs?: number
  reconnectMaxMs?: number
  heartbeatIntervalMs?: number
  heartbeatTimeoutMs?: number
  invokeTimeoutMs?: number
  connectTimeoutMs?: number
  maxReconnectAttempts?: number
  onError?: (err: Error) => void
  onReconnecting?: () => void
  useBinary?: boolean
  binaryOnly?: boolean
  schemaRegistry?: SchemaRegistryLike
  binaryCodec?: BinaryCodecLike
  /** Forwarded to ManagedFrameTransportOptions: fired on every transId
   *  sequence break so hosts can observe Wails-channel frame loss (see
   *  gapStats() for cumulative counters). Structurally matches the library's
   *  TransIdGapInfo (not re-exported from the package index). */
  onTransIdGap?: (info: { kind: string; got: bigint; expected: bigint; lost: number }) => void
}

export class WailsRawTransport extends ManagedFrameTransport implements ManagedTransport {
  constructor(options: WailsRawTransportOptions) {
    super({
      ...options,
      connection: new WailsRawFrameConnection(
        options.getAuthToken ?? (() => null),
      ),
      transportTag: 'wails-raw',
      // The Wails window event channel (ExecuteScript) can occasionally drop a
      // batch; a full reconnect would not recover it and the resubscribe storm
      // it triggers is worse than resyncing forward. See Trace-20260918 gap closes.
      gapPolicy: 'tolerate',
    } as ManagedFrameTransportOptions)
  }
}
