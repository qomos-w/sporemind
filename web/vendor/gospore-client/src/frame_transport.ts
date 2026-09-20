/**
 * ManagedFrameTransport — transport-agnostic session management over a raw
 * byte channel.
 *
 * Owns ALL shared connection lifecycle logic:
 *   - Reconnect with exponential backoff
 *   - Heartbeat ping/pong
 *   - Binary wire frame marshal/unmarshal + text JSON fallback
 *   - TransId gap detection
 *   - Auth handshake sequencing (delegates to WireSession)
 *   - Invoke queue flush (delegates to WireSession)
 *   - Subscription resume (delegates to WireSession)
 *
 * Does NOT own IO. The IO layer is a FrameConnection implementation:
 *   - WebSocketFrameConnection  → native browser WebSocket (ws.ts)
 *   - WailsFrameConnection      → Wails IPC bindings (wails_transport.ts)
 *
 * WebSocketTransport and WailsIpcTransport are thin subclasses that only
 * differ in which FrameConnection they construct — all network logic lives
 * here, exactly once.
 */

import type { InvokeOptions, ManagedTransport } from "./transport";
import {
  FrameType,
  PayloadEncoding,
  PayloadCompression,
  makeFlags,
  marshalWireFrame,
  unmarshalWireFrame,
  type WireFrame as BinaryWireFrame,
} from "./binary_frame";
import { compress } from "./compression";
import type { BinaryCodecLike, SchemaRegistryLike } from "./schema";
import { WireSession, type AppFrame, type TransIdGapKind, type TransIdGapVerdict } from "./session";
import type { WireFrame } from "./channel";

/* ------------------------------------------------------------------ */
/* FrameConnection — IO adapter interface                              */
/* ------------------------------------------------------------------ */

export type FrameConnectionState = "closed" | "connecting" | "open" | "closing" | "error" | "destroyed";

/**
 * IO adapter for ManagedFrameTransport.
 *
 * Implementations are responsible only for:
 *   - Establishing a connection when connect(url) is called
 *   - Sending raw bytes
 *   - Receiving raw bytes via the onmessage callback
 *   - Reporting open/close/error via callbacks
 *
 * No DOM events, no readyState constants, no WebSocket-specific semantics.
 */
export interface FrameConnection {
  readonly state: FrameConnectionState;
  connect(url: string): void;
  send(bytes: Uint8Array): void;
  close(code?: number, reason?: string): void;

  onopen: (() => void) | null;
  onmessage: ((data: Uint8Array) => void) | null;
  onclose: ((code: number, reason: string, wasClean: boolean) => void) | null;
  onerror: ((err: Error) => void) | null;
}

/* ------------------------------------------------------------------ */
/* Options                                                             */
/* ------------------------------------------------------------------ */

export interface ManagedFrameTransportOptions {
  connection: FrameConnection;
  url: string;
  getUrl?: () => string;
  getAuthToken?: () => string | null;
  onAuthFailure?: (err: Error) => void;
  lazyConnect?: boolean;
  reconnectBaseMs?: number;
  reconnectMaxMs?: number;
  reconnectJitterMs?: number;
  /** Connection must stay open this long before the reconnect backoff
   *  resets. Prevents hot reconnect loops when a connection opens and
   *  immediately dies (e.g. auth ok → sync burst → force-close). */
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
  /** Short label for this transport type, used in logs.
   *  Default: derived from the FrameConnection constructor name. */
  transportTag?: string;
  /** Policy when an inbound transId breaks the sequence. "close" (default)
   *  tears the connection down so reconnect resumes from a clean state —
   *  correct for reliable transports (TCP/WebSocket). "tolerate" drops
   *  duplicates and resyncs forward across skips, for transports whose
   *  event channel can occasionally lose a batch (Wails window events):
   *  a reconnect would not recover the lost frames either, and the
   *  resubscribe storm it triggers is worse than the gap. */
  gapPolicy?: "close" | "tolerate";
  /** Observability hook for transId gaps, fired on every skip/duplicate
   *  regardless of gapPolicy. The same counters are exposed via
   *  ManagedFrameTransport.gapStats(). Use this to quantify channel loss
   *  rate on tolerate-mode transports and decide when a reliable channel
   *  (WS) is needed for a stream. */
  onTransIdGap?: (info: TransIdGapInfo) => void;
}

/** Snapshot of one transId sequence break. */
export interface TransIdGapInfo {
  kind: TransIdGapKind;
  got: bigint;
  expected: bigint;
  /** Frames lost in this skip (0 for duplicates). */
  lost: number;
}

/* ------------------------------------------------------------------ */
/* ManagedFrameTransport                                               */
/* ------------------------------------------------------------------ */

export class ManagedFrameTransport implements ManagedTransport {
  private url: string;
  private getUrl?: () => string;
  private getAuthToken?: () => string | null;
  private reconnectBaseMs: number;
  private reconnectMaxMs: number;
  private reconnectJitterMs: number;
  private reconnectStableMs: number;
  private heartbeatIntervalMs: number;
  private heartbeatTimeoutMs: number;
  private invokeTimeoutMs: number;
  private connectTimeoutMs: number;

  protected connection: FrameConnection;
  private transportTag: string;
  private state: FrameConnectionState = "closed";
  private useBinary: boolean;
  private binaryOnly: boolean;
  private schemaRegistry?: SchemaRegistryLike;
  private binaryCodec?: BinaryCodecLike;
  private reconnectAttempts = 0;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private connectedAtMs: number | null = null;
  private heartbeatTimer: ReturnType<typeof setTimeout> | null = null;
  private heartbeatTimeoutTimer: ReturnType<typeof setTimeout> | null = null;
  private connectTimeoutTimer: ReturnType<typeof setTimeout> | null = null;

  private maxReconnectAttempts: number;
  private onError?: (err: Error) => void;
  private onReconnecting?: () => void;
  private gapPolicy: "close" | "tolerate";
  private onTransIdGapHook?: (info: TransIdGapInfo) => void;
  private gapSkipCount = 0;
  private gapDuplicateCount = 0;
  private gapLostFrames = 0;

  private onlineHandler: (() => void) | null = null;
  private visibilityHandler: (() => void) | null = null;

  private connectedHandlers = new Set<(info: { isReconnect: boolean }) => void>();
  private hasConnectedBefore = false;

  protected session: WireSession;

  /** Last received server TransID for text frames. Binary frames use
   *  WireSession.checkTransId internally. */
  private lastTransId: bigint | null = null;

  constructor(options: ManagedFrameTransportOptions) {
    this.url = options.url;
    this.getUrl = options.getUrl;
    this.connection = options.connection;
    this.getAuthToken = options.getAuthToken;
    this.reconnectBaseMs = options.reconnectBaseMs ?? 100;
    this.reconnectMaxMs = options.reconnectMaxMs ?? 3000;
    this.reconnectJitterMs = options.reconnectJitterMs ?? 0;
    this.reconnectStableMs = options.reconnectStableMs ?? 30000;
    this.heartbeatIntervalMs = options.heartbeatIntervalMs ?? 30000;
    this.heartbeatTimeoutMs = options.heartbeatTimeoutMs ?? 10000;
    this.invokeTimeoutMs = options.invokeTimeoutMs ?? 15000;
    this.connectTimeoutMs = options.connectTimeoutMs ?? 15000;
    this.maxReconnectAttempts = options.maxReconnectAttempts ?? Infinity;
    this.onError = options.onError;
    this.onReconnecting = options.onReconnecting;
    this.gapPolicy = options.gapPolicy ?? "close";
    this.onTransIdGapHook = options.onTransIdGap;
    this.binaryOnly = options.binaryOnly ?? false;
    this.useBinary = this.binaryOnly ? true : (options.useBinary ?? true);
    this.schemaRegistry = options.schemaRegistry;
    this.binaryCodec = options.binaryCodec;
    this.transportTag = options.transportTag ?? options.connection.constructor.name;

    this.connection.onopen = () => this.handleConnectionOpen();
    this.connection.onmessage = (data) => this.handleConnectionMessage(data);
    this.connection.onclose = (code, reason, wasClean) =>
      this.handleConnectionClose(code, reason, wasClean);
    this.connection.onerror = (err) => this.handleConnectionError(err);

    this.session = new WireSession({
      sendFrame: (frame) => this.sendAppFrame(frame),
      getAuthToken: options.getAuthToken,
      // Reject the open promise with the server-stated reason before
      // closeOnAuthFailure's generic "connection closed" rejection can win —
      // waitUntilReady callers must see *why* auth failed.
      onAuthFailure: (err) => {
        this.rejectOpenPromise(err);
        options.onAuthFailure?.(err);
      },
      binaryOnly: this.binaryOnly,
      schemaRegistry: this.schemaRegistry,
      binaryCodec: this.binaryCodec,
      invokeTimeoutMs: this.invokeTimeoutMs,
      onTransIdGap: (kind, got, expected) => this.handleTransIdGap(kind, got, expected),
      onAuthReady: () => {
        this.resolveOpenPromise();
        this.fireConnected();
      },
      closeOnAuthFailure: () => { this.connection.close(); },
    });

    // Network resilience: listen to browser online/visibility events so the
    // transport can react to network recovery and tab reactivation faster than
    // the reconnect timer or heartbeat interval would allow.
    if (typeof window !== "undefined") {
      this.onlineHandler = () => {
        if (this.state === "closed") {
          if (this.reconnectTimer) {
            clearTimeout(this.reconnectTimer);
            this.reconnectTimer = null;
          }
          console.log(`[${this.transportTag}] network online — reconnecting immediately`);
          this.connect();
        }
      };
      window.addEventListener("online", this.onlineHandler);

      this.visibilityHandler = () => {
        if (document.visibilityState === "visible" && this.state === "open") {
          this.sendHeartbeatPing();
        }
      };
      document.addEventListener("visibilitychange", this.visibilityHandler);
    }

    if (!options.lazyConnect) {
      this.connect();
    }
  }

  /* ------------------------------------------------------------------ */
  /* Public API — lifecycle                                              */
  /* ------------------------------------------------------------------ */

  waitForOpen(): Promise<void> {
    if (this.state === "open" && !this.session.authHandshake) {
      return Promise.resolve();
    }
    if (!this.openPromise) {
      this.openPromise = new Promise((resolve, reject) => {
        this.openResolve = resolve;
        this.openReject = reject;
      });
    }
    return this.openPromise;
  }

  waitUntilReady(): Promise<void> {
    return this.waitForOpen();
  }

  onConnected(handler: (info: { isReconnect: boolean }) => void): () => void {
    this.connectedHandlers.add(handler);
    return () => { this.connectedHandlers.delete(handler); };
  }

  private fireConnected(): void {
    const isReconnect = this.hasConnectedBefore;
    this.hasConnectedBefore = true;
    for (const h of this.connectedHandlers) {
      try { h({ isReconnect }); } catch (err) {
        console.error("onConnected handler threw:", err);
      }
    }
  }

  connect(): void {
    if (this.state !== "closed" && this.state !== "error") return;
    // NOTE: reconnectAttempts is deliberately NOT reset here. Resetting on
    // every connect collapses the exponential backoff back to base delay and
    // enables a hot reconnect loop. It is reset only when a connection has
    // been stable for reconnectStableMs (see handleConnectionClose).
    this.state = "connecting";

    const url = this.getUrl ? this.getUrl() : this.url;
    const tag = this.transportTag;
    console.log(`[${tag}] connecting to ${url} (attempt=${this.reconnectAttempts + 1})`);
    try {
      this.connection.connect(url);
      this.startConnectTimeout();
    } catch (err) {
      console.error(`[${this.transportTag}] connection construction failed:`, err);
      this.rejectOpenPromise(new Error("connection construction failed"));
      this.scheduleReconnect();
    }
  }

  reconnect(): void {
    if (this.state === "destroyed") return;
    this.connection.close();
    this.state = "closed";
    this.connect();
  }

  /* ------------------------------------------------------------------ */
  /* Public API — invoke / subscribe / close                             */
  /* ------------------------------------------------------------------ */

  async invoke(callID: string, payload: unknown, opts?: InvokeOptions): Promise<unknown> {
    if (this.state !== "connecting" && this.state !== "open") {
      throw new Error("transport not connected");
    }
    if (this.session.authHandshake) {
      await this.session.authHandshake;
    }
    if (this.state !== "connecting" && this.state !== "open") {
      throw new Error("transport not connected");
    }
    return this.session.invoke(callID, payload, opts);
  }

  subscribe(callID: string, payload: unknown, opts?: InvokeOptions): AsyncIterable<unknown> {
    if (this.state === "destroyed") {
      throw new Error("transport destroyed");
    }
    if (this.state === "closed") {
      this.connect();
    }
    return this.session.subscribe(callID, payload, opts);
  }

  close(): void {
    console.log(
      `[${this.transportTag}] transport close pending=${this.session.channel.pending.size} ` +
      `subs=${this.session.channel.subscriptions.size} queue=${this.session.channel.invokeQueue.length}`
    );
    this.rejectOpenPromise(new Error("transport closed"));
    this.state = "destroyed";
    this.connectedHandlers.clear();

    if (this.reconnectTimer) { clearTimeout(this.reconnectTimer); this.reconnectTimer = null; }
    this.clearConnectTimeout();
    this.stopHeartbeat();

    if (this.onlineHandler && typeof window !== "undefined") {
      window.removeEventListener("online", this.onlineHandler);
      this.onlineHandler = null;
    }
    if (this.visibilityHandler && typeof document !== "undefined") {
      document.removeEventListener("visibilitychange", this.visibilityHandler);
      this.visibilityHandler = null;
    }

    this.session.close();
    this.connection.close();
  }

  /* ------------------------------------------------------------------ */
  /* Connection callbacks                                               */
  /* ------------------------------------------------------------------ */

  private handleConnectionOpen(): void {
    if (this.state === "destroyed") return;
    this.clearConnectTimeout();
    this.state = "open";
    this.connectedAtMs = Date.now();
    this.lastTransId = null;
    console.log(`[${this.transportTag}] open url=${this.getUrl ? this.getUrl() : this.url}`);
    this.startHeartbeat();

    const token = this.getAuthToken ? this.getAuthToken() : null;
    if (token) {
      this.session.startAuth(token);
    } else {
      this.resolveOpenPromise();
      this.fireConnected();
    }

    this.session.resumeSubscriptions();
    this.flushInvokeQueue();
  }

  private handleConnectionMessage(data: Uint8Array): void {
    if (this.state === "destroyed") return;
    this.bumpHeartbeat();

    // Binary wire frame: starts with magic bytes GS F\x01.
    if (data.length >= 4 && data[0] === 0x47 && data[1] === 0x53 && data[2] === 0x46 && data[3] === 0x01) {
      const appFrame = this.handleBinaryMessage(data);
      if (appFrame) this.session.dispatchAppFrame(appFrame);
      return;
    }

    // JSON text frame fallback.
    if (this.binaryOnly) {
      console.error(`[${this.transportTag}] text frame received in binary-only mode; closing connection`);
      this.connection.close(4001, "text frame not allowed");
      return;
    }

    let frame: unknown;
    try {
      frame = JSON.parse(new TextDecoder().decode(data));
    } catch { return; }

    const transId = (frame as any).transId ?? 0n;
    if (typeof transId === "number") {
      if (!this.checkTransId(BigInt(transId))) return;
    } else if (typeof transId === "bigint") {
      if (!this.checkTransId(transId)) return;
    }
    this.session.dispatchAppFrame(frame as AppFrame);
  }

  private handleConnectionClose(code: number, reason: string, wasClean: boolean): void {
    if (this.state === "destroyed") return;
    this.clearConnectTimeout();
    // Idempotency: a forced reconnect or the connect-establishment timeout
    // may have already driven the close path. A late duplicate onclose from a
    // socket that has already transitioned to "closed" must not schedule a
    // second reconnect (which would race the in-flight one).
    if (this.state === "closed") return;
    const pendingCount = this.session.channel.pending.size;
    const subCount = this.session.channel.subscriptions.size;
    const queueCount = this.session.channel.invokeQueue.length;
    console.log(
      `[${this.transportTag}] closed code=${code} reason=${reason || "none"} wasClean=${wasClean} ` +
      `pending=${pendingCount} subs=${subCount} queue=${queueCount}`
    );
    this.rejectOpenPromise(new Error("connection closed"));
    this.state = "closed";
    this.stopHeartbeat();

    for (const q of this.session.channel.invokeQueue) {
      clearTimeout(q.timer);
      q.reject(new Error("connection closed"));
    }
    this.session.channel.invokeQueue = [];

    const captured = this.session.channel.capturePending();
    for (const c of captured) {
      this.session.channel.invokeQueue.push(c);
    }

    this.session.channel.notifySubscriptions();
    // Reset the backoff only if the connection was stable long enough.
    // Short-lived connections (open → burst → die) keep the accumulated
    // attempts so the next delay keeps growing exponentially.
    if (this.connectedAtMs !== null && Date.now() - this.connectedAtMs >= this.reconnectStableMs) {
      this.reconnectAttempts = 0;
    }
    this.connectedAtMs = null;
    this.scheduleReconnect();
  }

  private handleConnectionError(err: Error): void {
    console.error(`[${this.transportTag}] connection error:`, err);
  }

  /* ------------------------------------------------------------------ */
  /* Frame sending                                                       */
  /* ------------------------------------------------------------------ */

  private sendAppFrame(frame: WireFrame): boolean {
    if (this.connection.state !== "open") return false;

    if (this.useBinary) {
      const bwf = this.buildBinaryWireFrame(frame);
      if (!bwf) return false;
      if (this.connection.state !== "open") return false;
      this.connection.send(marshalWireFrame(bwf));
      return true;
    } else {
      this.connection.send(new TextEncoder().encode(JSON.stringify(frame)));
      return true;
    }
  }

  private buildBinaryWireFrame(frame: WireFrame): BinaryWireFrame | null {
    let type: FrameType;
    let corId = 0n;
    let seq = 0;
    let callId = "";
    let subId = "";
    let target = "";
    let errorMsg = "";
    let payloadBytes: Uint8Array = new Uint8Array(0);
    let payloadEncoding = PayloadEncoding.JSON;

    switch (frame.type) {
      case "invoke":
        type = FrameType.Invoke;
        corId = BigInt(frame.reqId);
        callId = frame.callID;
        target = frame.target ?? "";
        try {
          ({ bytes: payloadBytes, encoding: payloadEncoding } = this.session.encodePayload(
            frame.payload, frame.reqSchemaId, frame.namespace,
            { callID: frame.callID, target: frame.target, frameType: "invoke" },
          ));
        } catch (err) {
          if (frame.reqId !== undefined) {
            const pending = this.session.channel.pending.get(frame.reqId);
            if (pending) {
              this.session.channel.pending.delete(frame.reqId);
              pending.reject(err);
            }
          }
          return null;
        }
        break;
      case "reply":
        type = FrameType.Reply;
        corId = BigInt(frame.reqId);
        ({ bytes: payloadBytes } = this.session.encodePayload(frame.payload));
        break;
      case "error":
        type = FrameType.Error;
        corId = frame.reqId !== undefined ? BigInt(frame.reqId) : 0n;
        subId = frame.subId ?? "";
        errorMsg = frame.message ?? "";
        break;
      case "subscribe":
        type = FrameType.Subscribe;
        subId = frame.subId;
        callId = frame.callID;
        target = frame.target ?? "";
        seq = frame.sinceSeqNo;
        if (frame.payload !== undefined) {
          try {
            ({ bytes: payloadBytes, encoding: payloadEncoding } = this.session.encodePayload(
              frame.payload, frame.reqSchemaId, frame.namespace,
              { callID: frame.callID, target: frame.target, frameType: "subscribe" },
            ));
          } catch (err) {
            const sub = this.session.channel.subscriptions.get(frame.subId);
            if (sub) {
              sub.error = err instanceof Error ? err : new Error(String(err));
              sub.done = true;
              for (const it of sub.iterators) it.notify();
            }
            return null;
          }
        }
        break;
      case "unsubscribe":
        type = FrameType.Unsubscribe;
        subId = frame.subId;
        break;
      case "chunk":
        type = FrameType.Chunk;
        subId = frame.subId;
        seq = frame.seqNo;
        ({ bytes: payloadBytes } = this.session.encodePayload(frame.payload));
        break;
      case "end":
        type = FrameType.End;
        subId = frame.subId;
        break;
      case "auth":
        type = FrameType.Auth;
        if (typeof frame.payload === "string") {
          payloadBytes = new TextEncoder().encode(frame.payload);
        } else {
          payloadBytes = frame.payload;
        }
        payloadEncoding = PayloadEncoding.Binary;
        break;
      case "auth_ok":
        type = FrameType.AuthOk;
        break;
      case "ping":
        type = FrameType.Ping;
        break;
      case "pong":
        type = FrameType.Pong;
        break;
      default:
        return null;
    }

    const result = compress(payloadBytes);
    const comp = result.compressed ? PayloadCompression.Gzip : PayloadCompression.None;
    const flags = makeFlags(payloadEncoding, comp);

    return {
      flags, type, corId, seq, transId: 0n,
      callId, subId, target, from: "", errorMsg,
      payload: result.data,
      headers: new Map<string, string>(),
    };
  }

  /* ------------------------------------------------------------------ */
  /* Binary message handling                                             */
  /* ------------------------------------------------------------------ */

  private handleBinaryMessage(data: Uint8Array): AppFrame | null {
    try {
      const bf = unmarshalWireFrame(data);
      return this.session.receiveBinaryWireFrame(bf);
    } catch (err) {
      console.error(`[${this.transportTag}] binary message handling failed:`, err);
      return null;
    }
  }

  /* ------------------------------------------------------------------ */
  /* Heartbeat                                                           */
  /* ------------------------------------------------------------------ */

  private bumpHeartbeat(): void {
    if (this.heartbeatTimeoutTimer) {
      clearTimeout(this.heartbeatTimeoutTimer);
      this.heartbeatTimeoutTimer = null;
    }
  }

  private sendHeartbeatPing(): void {
    if (this.state !== "open") return;
    if (this.connection.state !== "open") return;
    this.sendAppFrame({ type: "ping" });
    if (this.heartbeatTimeoutTimer) clearTimeout(this.heartbeatTimeoutTimer);
    this.heartbeatTimeoutTimer = setTimeout(() => {
      console.warn(`[${this.transportTag}] heartbeat timeout after ${this.heartbeatTimeoutMs}ms, closing connection`);
      this.connection.close();
    }, this.heartbeatTimeoutMs);
  }

  private startHeartbeat(): void {
    this.stopHeartbeat();
    this.heartbeatTimer = setInterval(() => this.sendHeartbeatPing(), this.heartbeatIntervalMs);
  }

  private stopHeartbeat(): void {
    if (this.heartbeatTimer) { clearInterval(this.heartbeatTimer); this.heartbeatTimer = null; }
    if (this.heartbeatTimeoutTimer) { clearTimeout(this.heartbeatTimeoutTimer); this.heartbeatTimeoutTimer = null; }
  }

  // Connect-establishment timeout. Browsers do not impose a timeout on a
  // WebSocket stuck in CONNECTING: if the server (or a proxy) accepts the
  // TCP connection but never completes the upgrade handshake, onopen/onerror/
  // onclose never fire. Without this guard the transport would hang in
  // "connecting" forever — no heartbeat, no reconnect — which is exactly the
  // "stuck and never reconnects" failure mode. On expiry we close the hung
  // socket best-effort and drive the normal close/reconnect path directly.
  private startConnectTimeout(): void {
    this.clearConnectTimeout();
    this.connectTimeoutTimer = setTimeout(() => {
      if (this.state !== "connecting") return;
      console.warn(`[${this.transportTag}] connect handshake timeout after ${this.connectTimeoutMs}ms, retrying`);
      this.clearConnectTimeout();
      try { this.connection.close(4001, "connect timeout"); } catch { /* best effort */ }
      this.handleConnectionClose(4001, "connect timeout", false);
    }, this.connectTimeoutMs);
  }

  private clearConnectTimeout(): void {
    if (this.connectTimeoutTimer) { clearTimeout(this.connectTimeoutTimer); this.connectTimeoutTimer = null; }
  }

  /* ------------------------------------------------------------------ */
  /* Reconnect                                                           */
  /* ------------------------------------------------------------------ */

  private scheduleReconnect(): void {
    if (this.state !== "closed") return;
    if (this.reconnectAttempts >= this.maxReconnectAttempts) {
      const err = new Error(`reconnect exhausted after ${this.reconnectAttempts} attempt(s)`);
      console.error(`[${this.transportTag}] ${err.message}`);
      this.state = "error";
      this.rejectOpenPromise(err);
      this.onError?.(err);
      return;
    }
    const delay = Math.min(
      this.reconnectBaseMs * Math.pow(2, this.reconnectAttempts),
      this.reconnectMaxMs,
    );
    const jitter = Math.floor(Math.random() * this.reconnectJitterMs);
    const totalDelay = delay + jitter;
    this.reconnectAttempts++;
    console.log(`[${this.transportTag}] reconnect scheduled delay=${totalDelay}ms (base=${delay} jitter=${jitter}) attempt=${this.reconnectAttempts}`);
    this.onReconnecting?.();
    this.reconnectTimer = setTimeout(() => this.connect(), totalDelay);
  }

  /* ------------------------------------------------------------------ */
  /* TransId gap detection (text frames)                                 */
  /* ------------------------------------------------------------------ */

  private checkTransId(transId: bigint): boolean {
    if (transId === 0n) return true;
    if (this.lastTransId === null) {
      this.lastTransId = transId;
      return true;
    }
    const expected = this.lastTransId + 1n;
    if (transId !== expected) {
      const kind: TransIdGapKind = transId < expected ? "duplicate" : "skip";
      const verdict = this.handleTransIdGap(kind, transId, expected);
      if (verdict === "accept") {
        this.lastTransId = transId;
        return true;
      }
      return false;
    }
    this.lastTransId = transId;
    return true;
  }

  private handleTransIdGap(kind: TransIdGapKind, got: bigint, expected: bigint): TransIdGapVerdict {
    const lost = kind === "skip" ? Number(got - expected) : 0;
    if (kind === "skip") {
      this.gapSkipCount += 1;
      this.gapLostFrames += lost;
    } else {
      this.gapDuplicateCount += 1;
    }
    this.onTransIdGapHook?.({ kind, got, expected, lost });
    if (this.gapPolicy === "tolerate") {
      if (kind === "duplicate") {
        console.warn(`[${this.transportTag}] duplicate transId ${got} (expected ${expected}); dropping frame`);
        return "drop";
      }
      console.warn(`[${this.transportTag}] transId skip: got ${got}, expected ${expected}; lost frames tolerated, resyncing`);
      return "accept";
    }
    console.warn(`[${this.transportTag}] transId gap detected: got ${got}, expected ${expected}; closing connection`);
    this.connection.close(4001, "transId gap");
    return "close";
  }

  /** Cumulative transId gap counters for this transport instance
   *  (across reconnects — the channel's loss rate is the property being
   *  measured, not one connection's). */
  gapStats(): { skips: number; duplicates: number; lostFrames: number } {
    return {
      skips: this.gapSkipCount,
      duplicates: this.gapDuplicateCount,
      lostFrames: this.gapLostFrames,
    };
  }

  /* ------------------------------------------------------------------ */
  /* Open promise                                                        */
  /* ------------------------------------------------------------------ */

  private openPromise: Promise<void> | null = null;
  private openResolve: (() => void) | null = null;
  private openReject: ((err: Error) => void) | null = null;

  private resolveOpenPromise(): void {
    this.openResolve?.();
    this.openPromise = null;
    this.openResolve = null;
    this.openReject = null;
  }

  private rejectOpenPromise(err: Error): void {
    if (this.openPromise) {
      this.openReject?.(err);
      this.openPromise = null;
      this.openResolve = null;
      this.openReject = null;
    }
  }

  /* ------------------------------------------------------------------ */
  /* Invoke queue flush                                                  */
  /* ------------------------------------------------------------------ */

  private flushInvokeQueue(): void {
    const flush = () => {
      const queue = this.session.channel.invokeQueue;
      const remaining: typeof queue = [];
      for (const q of queue) {
        clearTimeout(q.timer);
        if (this.state !== "open" || this.connection.state !== "open") {
          remaining.push(q);
          continue;
        }
        const timeoutMs = q.frame.timeoutMs ?? this.invokeTimeoutMs;
        const timer = setTimeout(() => {
          if (this.session.channel.pending.has(q.frame.reqId)) {
            this.session.channel.pending.delete(q.frame.reqId);
            q.reject(new Error(`invoke ${q.frame.callID} timed out`));
          }
        }, timeoutMs);
        this.session.channel.pending.set(q.frame.reqId, {
          callID: q.frame.callID,
          frame: q.frame,
          resolve: (value) => { clearTimeout(timer); q.resolve(value); },
          reject: (reason) => { clearTimeout(timer); q.reject(reason); },
          timer,
        });
        if (!this.sendAppFrame(q.frame)) {
          this.session.channel.pending.delete(q.frame.reqId);
          clearTimeout(timer);
          remaining.push(q);
        }
      }
      this.session.channel.invokeQueue = remaining;
    };

    if (this.session.authHandshake) {
      this.session.authHandshake.then(flush).catch(() => {
        for (const q of this.session.channel.invokeQueue) {
          clearTimeout(q.timer);
          q.reject(new Error("auth failed"));
        }
        this.session.channel.invokeQueue = [];
      });
    } else {
      flush();
    }
  }
}
