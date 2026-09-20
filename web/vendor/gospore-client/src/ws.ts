/**
 * WebSocket transport for gospore-client.
 *
 * WebSocketFrameConnection adapts the native browser WebSocket API to the
 * FrameConnection byte-channel interface. WebSocketTransport is a thin
 * subclass of ManagedFrameTransport that injects a WebSocketFrameConnection
 * — all network/session logic lives in the shared base class.
 */

import {
  ManagedFrameTransport,
  type FrameConnection,
  type FrameConnectionState,
} from "./frame_transport";
import type { SchemaRegistryLike, BinaryCodecLike } from "./schema";
import type { TransIdGapInfo } from "./frame_transport";

/* ------------------------------------------------------------------ */
/* WebSocketLike — kept for backward compatibility with test mocks      */
/* ------------------------------------------------------------------ */

export interface WebSocketLike {
  readonly readyState: number;
  binaryType: BinaryType;
  onopen: ((event: Event) => void) | null;
  onmessage: ((event: MessageEvent) => void) | null;
  onclose: ((event: CloseEvent) => void) | null;
  onerror: ((event: Event) => void) | null;
  send(data: string | ArrayBufferLike | Blob | ArrayBufferView): void;
  close(code?: number, reason?: string): void;
}

/* ------------------------------------------------------------------ */
/* WebSocketFrameConnection                                            */
/* ------------------------------------------------------------------ */

/**
 * Adapts a native browser WebSocket (or any WebSocketLike) to the
 * FrameConnection byte-channel interface.
 *
 * Responsibilities:
 *   - Create the WebSocket via createSocket(url)
 *   - Convert incoming MessageEvent.data (string | ArrayBuffer) → Uint8Array
 *   - Convert outgoing Uint8Array → ArrayBuffer
 *   - Translate DOM events to FrameConnection callbacks
 */
export class WebSocketFrameConnection implements FrameConnection {
  private ws: WebSocketLike | null = null;
  private createSocket: (url: string) => WebSocketLike;

  onopen: (() => void) | null = null;
  onmessage: ((data: Uint8Array) => void) | null = null;
  onclose: ((code: number, reason: string, wasClean: boolean) => void) | null = null;
  onerror: ((err: Error) => void) | null = null;

  constructor(createSocket?: (url: string) => WebSocketLike) {
    this.createSocket = createSocket ?? ((url) => new WebSocket(url));
  }

  get state(): FrameConnectionState {
    if (!this.ws) return "closed";
    switch (this.ws.readyState) {
      case 0: return "connecting";
      case 1: return "open";
      case 2: return "closing";
      default: return "closed";
    }
  }

  connect(url: string): void {
    // Detach + close any previous socket before replacing it. A forced
    // reconnect closes the old socket but its close event arrives
    // asynchronously; if we just overwrote this.ws, the late close/message
    // events would still reach the transport state machine (stomping the new
    // connection back to "closed") or feed frames into the shared session
    // (transId gaps → reconnect storm).
    const prev = this.ws;
    if (prev) {
      prev.onopen = null;
      prev.onmessage = null;
      prev.onclose = null;
      prev.onerror = null;
      this.ws = null;
      if (prev.readyState === 0 || prev.readyState === 1) {
        try {
          prev.close();
        } catch {
          // best effort — the socket is detached either way
        }
      }
    }

    const ws = this.createSocket(url);
    this.ws = ws;
    ws.binaryType = "arraybuffer";

    ws.onopen = () => {
      if (this.ws !== ws) return;
      this.onopen?.();
    };

    ws.onmessage = (ev: MessageEvent) => {
      if (this.ws !== ws) return;
      let bytes: Uint8Array;
      if (typeof ev.data === "string") {
        bytes = new TextEncoder().encode(ev.data);
      } else if (ev.data instanceof ArrayBuffer) {
        bytes = new Uint8Array(ev.data);
      } else if (ArrayBuffer.isView(ev.data)) {
        bytes = new Uint8Array(ev.data.buffer, ev.data.byteOffset, ev.data.byteLength);
      } else {
        return;
      }
      this.onmessage?.(bytes);
    };

    ws.onclose = (ev: CloseEvent) => {
      if (this.ws !== ws) return;
      this.onclose?.(ev.code, ev.reason, ev.wasClean);
    };

    ws.onerror = () => {
      if (this.ws !== ws) return;
      this.onerror?.(new Error("websocket error"));
    };
  }

  send(bytes: Uint8Array): void {
    if (!this.ws || this.ws.readyState !== 1) {
      throw new Error("WebSocket is not open");
    }
    const buf = bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) as ArrayBuffer;
    this.ws.send(buf);
  }

  close(code?: number, reason?: string): void {
    this.ws?.close(code, reason);
  }
}

/* ------------------------------------------------------------------ */
/* WebSocketTransport — thin ManagedFrameTransport subclass             */
/* ------------------------------------------------------------------ */

export interface WebSocketTransportOptions {
  url: string;
  createSocket?: (url: string) => WebSocketLike;
  getUrl?: () => string;
  getAuthToken?: () => string | null;
  onAuthFailure?: (err: Error) => void;
  lazyConnect?: boolean;
  reconnectBaseMs?: number;
  reconnectMaxMs?: number;
  reconnectJitterMs?: number;
  /** Connection must stay open this long before the reconnect backoff
   *  resets (see ManagedFrameTransportOptions). */
  reconnectStableMs?: number;
  heartbeatIntervalMs?: number;
  heartbeatTimeoutMs?: number;
  invokeTimeoutMs?: number;
  connectTimeoutMs?: number;
  maxReconnectAttempts?: number;
  onError?: (err: Error) => void;
  onReconnecting?: () => void;
  useBinary?: boolean;
  binaryOnly?: boolean;
  schemaRegistry?: SchemaRegistryLike;
  binaryCodec?: BinaryCodecLike;
  /** See ManagedFrameTransportOptions.gapPolicy (default "close"). */
  gapPolicy?: "close" | "tolerate";
  /** See ManagedFrameTransportOptions.onTransIdGap. */
  onTransIdGap?: (info: TransIdGapInfo) => void;
  transportTag?: string;
}

export class WebSocketTransport extends ManagedFrameTransport {
  constructor(options: WebSocketTransportOptions) {
    super({
      ...options,
      connection: new WebSocketFrameConnection(options.createSocket),
      transportTag: "ws",
    });
  }
}
