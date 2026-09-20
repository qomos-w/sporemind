/**
 * Wails IPC transport for gospore-client.
 *
 * WailsFrameConnection adapts Wails IPC bindings to the FrameConnection
 * byte-channel interface. WailsIpcTransport is a thin subclass of
 * ManagedFrameTransport that injects a WailsFrameConnection — all
 * network/session logic is shared with WebSocketTransport via the base class.
 *
 * Unlike the old WailsSocket approach, this does NOT synthesize DOM events
 * (MessageEvent, CloseEvent, readyState constants). It works purely with
 * bytes and plain callbacks.
 */

import {
  ManagedFrameTransport,
  type FrameConnection,
  type FrameConnectionState,
} from "./frame_transport";
import type { SchemaRegistryLike, BinaryCodecLike } from "./schema";

/* ------------------------------------------------------------------ */
/* WailsBindings — injected Wails runtime dependencies                 */
/* ------------------------------------------------------------------ */

/**
 * Wails IPC binding functions injected by the host application.
 * gospore-client itself does not depend on @wailsio/runtime.
 */
export interface WailsBindings {
  /** Establish a backend IPC session. Returns a session ID. */
  connect(token: string): Promise<string>;
  /** Send raw bytes (base64-encoded) to the backend for the given session. */
  send(sessionId: string, base64Data: string): Promise<void>;
  /** Close the backend session. */
  close(sessionId: string): Promise<void>;
  /** Register a listener for a named event. Returns a cancel function. */
  on(event: string, handler: (data: unknown) => void): () => void;
}

/* ------------------------------------------------------------------ */
/* WailsFrameConnection                                                */
/* ------------------------------------------------------------------ */

const DEFAULT_FRAME_EVENT = "sporecode:transport:frame";
const DEFAULT_CLOSE_EVENT = "sporecode:transport:close";

interface TransportBatch {
  sessionId: string;
  frames: { data: string }[];
}

function bytesToBase64(data: Uint8Array): string {
  let binary = "";
  for (let offset = 0; offset < data.length; offset += 0x8000) {
    binary += String.fromCharCode(...data.subarray(offset, offset + 0x8000));
  }
  return btoa(binary);
}

function base64ToBytes(data: string): Uint8Array {
  const binary = atob(data);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

export class WailsFrameConnection implements FrameConnection {
  private bindings: WailsBindings;
  private getToken: () => string | null;
  private frameEvent: string;
  private closeEvent: string;
  private sessionId = "";
  private _state: FrameConnectionState = "closed";
  private cancelFrame: (() => void) | null = null;
  private cancelClose: (() => void) | null = null;

  onopen: (() => void) | null = null;
  onmessage: ((data: Uint8Array) => void) | null = null;
  onclose: ((code: number, reason: string, wasClean: boolean) => void) | null = null;
  onerror: ((err: Error) => void) | null = null;

  constructor(
    bindings: WailsBindings,
    getToken: () => string | null,
    frameEvent = DEFAULT_FRAME_EVENT,
    closeEvent = DEFAULT_CLOSE_EVENT,
  ) {
    this.bindings = bindings;
    this.getToken = getToken;
    this.frameEvent = frameEvent;
    this.closeEvent = closeEvent;
  }

  get state(): FrameConnectionState {
    return this._state;
  }

  connect(_url: string): void {
    this._state = "connecting";

    this.cancelFrame = this.bindings.on(this.frameEvent, (event: unknown) => {
      const value = event as { data?: unknown };
      const batch = (typeof value.data === "object" ? value.data : event) as TransportBatch;
      if (batch.sessionId !== this.sessionId || this._state !== "open") return;
      // Process frames in array order — guaranteed by Go-side batching.
      for (const f of batch.frames) {
        this.onmessage?.(base64ToBytes(f.data));
      }
    });

    this.cancelClose = this.bindings.on(this.closeEvent, (event: unknown) => {
      const value = event as { data?: unknown };
      const sid = typeof value.data === "string" ? value.data : event;
      if (sid === this.sessionId) this.finishClose(1006, "backend transport closed", false);
    });

    void this.doConnect();
  }

  private async doConnect(): Promise<void> {
    try {
      this.sessionId = await this.bindings.connect(this.getToken() ?? "");
      if (this._state !== "connecting") {
        await this.bindings.close(this.sessionId);
        return;
      }
      this._state = "open";
      console.log(`[wails-ipc] backend connect OK, sessionId=${this.sessionId}`);
      this.onopen?.();
    } catch (err) {
      console.error("[wails-ipc] backend connect error:", err);
      this.onerror?.(err instanceof Error ? err : new Error("backend connect failed"));
      this.finishClose(1006, "backend connect failed", false);
    }
  }

  send(bytes: Uint8Array): void {
    if (this._state !== "open") {
      throw new Error("WailsFrameConnection is not open");
    }
    void this.bindings.send(this.sessionId, bytesToBase64(bytes)).catch(() => {
      this.onerror?.(new Error("backend send failed"));
      this.finishClose(1006, "backend send failed", false);
    });
  }

  close(code?: number, _reason?: string): void {
    if (this._state === "closing" || this._state === "closed") return;
    this._state = "closing";
    if (this.sessionId) void this.bindings.close(this.sessionId);
    this.finishClose(code ?? 1000, _reason ?? "", true);
  }

  private finishClose(code: number, reason: string, wasClean: boolean): void {
    if (this._state === "closed") return;
    this._state = "closed";
    this.cancelFrame?.();
    this.cancelClose?.();
    this.onclose?.(code, reason, wasClean);
  }
}

/* ------------------------------------------------------------------ */
/* WailsIpcTransport — thin ManagedFrameTransport subclass             */
/* ------------------------------------------------------------------ */

export interface WailsIpcTransportOptions {
  url: string;
  bindings: WailsBindings;
  getAuthToken?: () => string | null;
  getUrl?: () => string;
  onAuthFailure?: (err: Error) => void;
  lazyConnect?: boolean;
  reconnectBaseMs?: number;
  reconnectMaxMs?: number;
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
}

export class WailsIpcTransport extends ManagedFrameTransport {
  constructor(options: WailsIpcTransportOptions) {
    super({
      ...options,
      connection: new WailsFrameConnection(options.bindings, options.getAuthToken ?? (() => null)),
      transportTag: "wails-ipc",
    });
  }
}
