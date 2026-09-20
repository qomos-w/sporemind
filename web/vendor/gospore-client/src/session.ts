/**
 * WireSession — application-level session over a raw frame channel.
 *
 * Owns:
 *   - Auth handshake (auth frame → wait for auth_ok → resolve authPromise)
 *   - Callable-schema cache (parsed from auth_ok Manifest)
 *   - Binary payload encode/decode (via schemaRegistry + binaryCodec)
 *   - System-protocol builtin encoding (event subscribe, projection, auth)
 *   - Frame dispatch routing (auth_ok handled here, rest delegated to FrameChannel)
 *
 * Does NOT own:
 *   - Wire-protocol marshal/unmarshal (binary_frame.ts)
 *   - WebSocket lifecycle, heartbeat, reconnect
 *
 * The owning WebSocketTransport provides sendAppFrame to handle serialization.
 */

import type { InvokeOptions } from "./transport";
import type { BinaryCodecLike, SchemaRegistryLike, SchemaEntry, ObjectDesc } from "./schema";
import {
  PayloadEncoding,
  PayloadCompression,
  getEncoding,
  getCompression,
  FrameType,
  type WireFrame as BinaryWireFrame,
} from "./binary_frame";
import { decompress } from "./compression";
import {
  FrameChannel,
  SubscriptionIterator,
  type WireFrame,
  type InvokeFrame,
  type SubscribeFrame,
} from "./channel";

/* ------------------------------------------------------------------ */
/* System-reserved schema IDs, TypeDescs and ObjectDescs               */
/* ------------------------------------------------------------------ */
/* Generated from gospore schema/builtin.go — single source of truth   */
/* shared with the Go gateway. Regenerate via                          */
/*   go test ./internal/wiregen -update                                */
import {
  SYS_AUTH_REQ,
  SYS_AUTH_OK,
  SYS_EVENT_SUBSCRIBE_SERVICE_REQ,
  SYS_EVENT_SUBSCRIBE_INSTANCE_REQ,
  SYS_PROJECTION_GET_REQ,
  SYS_PROJECTION_WATCH_REQ,
  AUTH_REQ_DESC,
  AUTH_OK_DESC,
  EVENT_SUBSCRIBE_SERVICE_REQ_DESC,
  EVENT_SUBSCRIBE_INSTANCE_REQ_DESC,
  PROJECTION_GET_REQ_DESC,
  PROJECTION_WATCH_REQ_DESC,
  AUTH_REQ_OBJECT,
  AUTH_OK_OBJECT,
  EVENT_SUBSCRIBE_SERVICE_REQ_OBJECT,
  EVENT_SUBSCRIBE_INSTANCE_REQ_OBJECT,
  PROJECTION_GET_REQ_OBJECT,
  PROJECTION_WATCH_REQ_OBJECT,
} from "./generated/system_protocol";

const sysObjectFor = (classId: number): ((name: string) => ObjectDesc | undefined) => {
  const map = new Map<string, ObjectDesc>();
  if (classId === SYS_AUTH_REQ) map.set("AuthReq", AUTH_REQ_OBJECT);
  if (classId === SYS_AUTH_OK) map.set("AuthOk", AUTH_OK_OBJECT);
  if (classId === SYS_EVENT_SUBSCRIBE_SERVICE_REQ) map.set("eventSubscribeServiceReq", EVENT_SUBSCRIBE_SERVICE_REQ_OBJECT);
  if (classId === SYS_EVENT_SUBSCRIBE_INSTANCE_REQ) map.set("eventSubscribeInstanceReq", EVENT_SUBSCRIBE_INSTANCE_REQ_OBJECT);
  if (classId === SYS_PROJECTION_GET_REQ) map.set("projectionGetReq", PROJECTION_GET_REQ_OBJECT);
  if (classId === SYS_PROJECTION_WATCH_REQ) map.set("projectionWatchReq", PROJECTION_WATCH_REQ_OBJECT);
  return (name: string) => map.get(name);
};

// TBC magic preamble. The wire format split the version out of the magic
// (spore v0.6.0 / #17): every frame now starts with "TBC" + an explicit
// version byte, so recognition must check the 3-byte preamble only.
// Version semantics belong to the codec backend, mirroring the Go-side
// gospore codec.IsTBCData preamble fix.
const TBC_MAGIC = new Uint8Array([0x54, 0x42, 0x43]);
const TBC_HEADER_LEN = TBC_MAGIC.length + 1; // preamble + version byte
const TAG_STRUCT = 0x0c;

function isTBCPayload(data: Uint8Array): boolean {
  return data.length >= TBC_HEADER_LEN && data[0] === TBC_MAGIC[0] && data[1] === TBC_MAGIC[1] && data[2] === TBC_MAGIC[2];
}

function tbcStructSchemaId(data: Uint8Array): number {
  if (data.length < TBC_HEADER_LEN + 1) return 0;
  if (data[0] !== TBC_MAGIC[0] || data[1] !== TBC_MAGIC[1] || data[2] !== TBC_MAGIC[2]) return 0;
  if (data[TBC_HEADER_LEN] !== TAG_STRUCT) return 0;
  let off = TBC_HEADER_LEN + 1;
  let result = 0;
  let shift = 0;
  while (off < data.length) {
    const b = data[off++]!;
    result |= (b & 0x7f) << shift;
    if ((b & 0x80) === 0) break;
    shift += 7;
  }
  return result;
}

/* ------------------------------------------------------------------ */
/* CallableSchemaMeta                                                 */
/* ------------------------------------------------------------------ */

interface CallableSchemaMeta {
  reqSchemaId: number;
  finalSchemaId: number;
  chunkSchemaId?: number;
  namespace: string;
  mode: string;
}

/* ------------------------------------------------------------------ */
/* WireSessionOptions                                                 */
/* ------------------------------------------------------------------ */

export interface WireSessionOptions {
  /** Called to send an application-level frame. The transport
   *  serializes it (binary or JSON) and sends over the wire.
   *  Returns true if the frame was handed to the transport. */
  sendFrame: (frame: WireFrame) => boolean | Promise<void>;
  /** Return the current auth token. When set, an auth handshake is sent
   *  after the socket opens. */
  getAuthToken?: () => string | null;
  /** Called when the server rejects the auth handshake; receives the
   *  server-stated reason (or "auth handshake timeout"). */
  onAuthFailure?: (err: Error) => void;
  /** When true, reject JSON fallback paths. */
  binaryOnly?: boolean;
  /** Schema registry for binary codec type lookups. */
  schemaRegistry?: SchemaRegistryLike;
  /** Binary codec for encoding/decoding TBC-format payloads. */
  binaryCodec?: BinaryCodecLike;
  /** Per-invoke response timeout in ms. Default 15000. */
  invokeTimeoutMs?: number;
  /** Called when an inbound transId breaks the sequence: "skip" means
   *  frames were lost (got > expected), "duplicate" means a frame was
   *  redelivered (got < expected). The return value decides the fate of
   *  the offending frame: "accept" resyncs forward and processes it,
   *  "drop" skips it, "close" falls back to the legacy teardown (the
   *  transport closes the connection so reconnect resumes from a clean
   *  state). */
  onTransIdGap?: (kind: TransIdGapKind, got: bigint, expected: bigint) => TransIdGapVerdict;
  /** Called when auth handshake succeeds (after manifest processing). */
  onAuthReady?: () => void;
  /** Called when auth fails and the transport should close its connection. */
  closeOnAuthFailure?: () => void;
}

/* ------------------------------------------------------------------ */
/* WireSession                                                        */
/* ------------------------------------------------------------------ */

export { SubscriptionIterator };
export type { WireFrame as AppFrame };

export type TransIdGapKind = "skip" | "duplicate";
export type TransIdGapVerdict = "accept" | "drop" | "close";

export class WireSession {
  readonly channel: FrameChannel;

  private binaryOnly: boolean;
  private schemaRegistry?: SchemaRegistryLike;
  private binaryCodec?: BinaryCodecLike;
  private invokeTimeoutMs: number;

  private onAuthFailure?: (err: Error) => void;
  private onTransIdGap?: (kind: TransIdGapKind, got: bigint, expected: bigint) => TransIdGapVerdict;
  private onAuthReady?: () => void;
  private closeOnAuthFailure?: () => void;

  /** Last received server TransID; used to detect dropped frames. */
  private lastTransId: bigint | null = null;

  /** Tracks response schema info per pending request for binary decode. */
  private pendingResInfo = new Map<number, { namespace: string; resSchemaId: number }>();
  /** Tracks chunk schema info per subscription for binary decode. */
  private subChunkInfo = new Map<string, { namespace: string; chunkSchemaId: number }>();

  /** Callable schema map resolved from auth_ok Manifest. */
  private callableSchemaCache = new Map<string, CallableSchemaMeta>();

  /** Auth handshake promise. Resolves when auth_ok arrives. */
  private authPromise: Promise<void> | null = null;
  private authResolve: (() => void) | null = null;
  private authReject: ((err: Error) => void) | null = null;
  private authTimeoutTimer: ReturnType<typeof setTimeout> | null = null;

  constructor(options: WireSessionOptions) {
    this.onAuthFailure = options.onAuthFailure;
    this.binaryOnly = options.binaryOnly ?? false;
    this.schemaRegistry = options.schemaRegistry;
    this.binaryCodec = options.binaryCodec;
    this.invokeTimeoutMs = options.invokeTimeoutMs ?? 15000;
    this.onTransIdGap = options.onTransIdGap;
    this.onAuthReady = options.onAuthReady;
    this.closeOnAuthFailure = options.closeOnAuthFailure;

    this.channel = new FrameChannel();
    this.channel.send = (frame) => options.sendFrame(frame);
    this.channel.sendUnsubscribe = (subId) => {
      this.channel.send?.({ type: "unsubscribe", subId });
    };
  }

  /* ------------------------------------------------------------------ */
  /* Public API — invoke, subscribe, close                              */
  /* ------------------------------------------------------------------ */

  async invoke(callID: string, payload: unknown, opts?: InvokeOptions): Promise<unknown> {
    if (this.authPromise) {
      await this.authPromise;
    }

    const reqId = this.channel.nextReqId++;
    const cached = this.resolveCallableSchema(callID);
    const namespace = cached?.namespace ?? callID.substring(0, callID.lastIndexOf(".")) ?? callID;
    const frame: InvokeFrame = { type: "invoke", reqId, callID, payload };
    if (opts?.target) frame.target = opts.target;
    frame.reqSchemaId = this.resolveReqSchemaId(callID, opts);
    frame.namespace = namespace;
    frame.timeoutMs = opts?.timeoutMs ?? this.invokeTimeoutMs;

    const resSchemaId = opts?.resSchemaId ?? cached?.finalSchemaId;
    if (resSchemaId !== undefined) {
      this.pendingResInfo.set(reqId, { namespace, resSchemaId });
    }

    const timeoutMs = opts?.timeoutMs ?? this.invokeTimeoutMs;

    return new Promise<unknown>((resolve, reject) => {
      this.channel.sendInvoke(frame, timeoutMs, resolve, reject);
    });
  }

  subscribe(callID: string, payload: unknown, opts?: InvokeOptions): AsyncIterable<unknown> {
    const subId = `${callID}:${this.channel.nextReqId++}`;
    const cached = this.resolveCallableSchema(callID);
    const namespace = cached?.namespace ?? callID.substring(0, callID.lastIndexOf(".")) ?? callID;
    const reqSchemaId = this.resolveReqSchemaId(callID, opts);

    const sub = {
      callID,
      payload,
      target: opts?.target,
      reqSchemaId,
      sinceSeqNo: 0,
      withSeqNo: opts?.withSeqNo ?? false,
      queue: [] as Array<{seqNo: number; payload: unknown}>,
      error: null as Error | null,
      done: false,
      iterators: new Set<SubscriptionIterator>(),
    };

    const chunkSchemaId = opts?.chunkSchemaId ?? cached?.chunkSchemaId;
    if (chunkSchemaId !== undefined) {
      this.subChunkInfo.set(subId, { namespace, chunkSchemaId });
    }

    const iterable = this.channel.createSubscription(subId, sub);

    const sendSub = () => {
      // The subscription may have been fully disposed while we waited for
      // auth: dispose() already sent an unsubscribe (a backend no-op, since
      // the subscribe had not been sent yet) and removed the channel entry.
      // Sending the deferred subscribe now would create a backend
      // subscription nobody will ever cancel — a permanent goroutine and
      // stream leak. Abort instead.
      if (this.channel.subscriptions.get(subId) !== sub) return;
      const frame: SubscribeFrame = { type: "subscribe", subId, callID, payload, sinceSeqNo: 0 };
      if (sub.target) frame.target = sub.target;
      frame.reqSchemaId = reqSchemaId;
      frame.namespace = namespace;
      if (!this.channel.send || !this.channel.send(frame)) {
        sub.error = new Error("websocket not connected");
        sub.done = true;
        for (const it of sub.iterators) it.notify();
      }
    };

    if (!this.authPromise) {
      sendSub();
    } else {
      this.authPromise.then(sendSub).catch(() => {
        sub.error = new Error("auth failed");
        sub.done = true;
        for (const it of sub.iterators) it.notify();
      });
    }

    return iterable;
  }

  close(): void {
    this.cleanupAuth();
    this.channel.close(new Error("transport closed"));
    this.pendingResInfo.clear();
    this.subChunkInfo.clear();
  }

  /* ------------------------------------------------------------------ */
  /* Auth handshake                                                     */
  /* ------------------------------------------------------------------ */

  /** Returns the auth promise, or null if no auth is configured. */
  get authHandshake(): Promise<void> | null {
    return this.authPromise;
  }

  /** Initiate the auth handshake. Called by the transport after the
   *  WebSocket opens when a token provider is configured. */
  startAuth(token: string): void {
    this.lastTransId = null;

    this.authPromise = new Promise((resolve, reject) => {
      this.authResolve = resolve;
      this.authReject = reject;
    });

    this.authTimeoutTimer = setTimeout(() => {
      const err = new Error("auth handshake timeout");
      this.authReject?.(err);
      this.cleanupAuth();
      this.onAuthFailure?.(err);
      this.closeOnAuthFailure?.();
    }, 5000);

    this.channel.send?.({ type: "auth", payload: this.encodeAuthReq(token) });
  }

  private cleanupAuth(): void {
    if (this.authTimeoutTimer) {
      clearTimeout(this.authTimeoutTimer);
      this.authTimeoutTimer = null;
    }
    this.authPromise = null;
    this.authResolve = null;
    this.authReject = null;
  }

  /* ------------------------------------------------------------------ */
  /* Incoming frame processing                                          */
  /* ------------------------------------------------------------------ */

  /**
   * Process an incoming binary wire frame (already unmarshalled).
   * Decodes the payload and returns an application-level WireFrame,
   * or null if the frame was rejected / handled internally.
   */
  receiveBinaryWireFrame(bf: BinaryWireFrame): WireFrame | null {
    if (!this.checkTransId(bf.transId)) return null;

    const comp = getCompression(bf.flags);
    const enc = getEncoding(bf.flags);

    let payloadBytes = bf.payload;
    if (comp === PayloadCompression.Gzip) {
      try {
        payloadBytes = decompress(payloadBytes);
      } catch {
        return null;
      }
    }

    switch (bf.type) {
      case FrameType.Reply: {
        const reqId = Number(bf.corId);
        try {
          const payload = this.decodePayload(payloadBytes, enc, reqId, undefined);
          return { type: "reply", reqId, payload };
        } catch (err) {
          const pending = this.channel.pending.get(reqId);
          if (pending) {
            this.channel.pending.delete(reqId);
            this.pendingResInfo.delete(reqId);
            pending.reject(err);
          }
          return null;
        }
      }
      case FrameType.Error:
        return {
          type: "error",
          reqId: bf.corId !== 0n ? Number(bf.corId) : undefined,
          subId: bf.subId || undefined,
          message: bf.errorMsg || undefined,
        };
      case FrameType.Chunk: {
        try {
          const payload = this.decodePayload(payloadBytes, enc, undefined, bf.subId);
          return { type: "chunk", subId: bf.subId, seqNo: bf.seq, payload };
        } catch (err) {
          const sub = this.channel.subscriptions.get(bf.subId);
          if (sub) {
            sub.error = err instanceof Error ? err : new Error(String(err));
            sub.done = true;
            for (const it of sub.iterators) it.notify();
          }
          return null;
        }
      }
      case FrameType.End:
        return { type: "end", subId: bf.subId };
      case FrameType.AuthOk: {
        let authOkPayload: unknown = undefined;
        if (this.binaryCodec && payloadBytes.length > 0) {
          try {
            const of = sysObjectFor(SYS_AUTH_OK);
            authOkPayload = this.binaryCodec.decode(AUTH_OK_DESC, payloadBytes, of);
          } catch {
            if (!this.binaryOnly) {
              try { authOkPayload = JSON.parse(new TextDecoder().decode(payloadBytes)); } catch { /* ignore */ }
            }
          }
        }
        return { type: "auth_ok", payload: authOkPayload };
      }
      case FrameType.Pong:
        return { type: "pong" };
      case FrameType.Ping:
        return { type: "ping" };
      default:
        return null;
    }
  }

  /**
   * Dispatch an application-level frame (parsed from JSON text or
   * decoded from binary). Handles auth_ok and pong, delegates the
   * rest to FrameChannel.dispatch.
   */
  dispatchAppFrame(frame: WireFrame): void {
    switch (frame.type) {
      case "auth_ok":
        this.processAuthOk(frame.payload);
        this.authResolve?.();
        this.cleanupAuth();
        this.onAuthReady?.();
        break;
      case "error": {
        // If auth is pending and no reqId, treat as auth failure.
        if (this.authPromise && !frame.reqId) {
          const err = new Error(frame.message ?? "auth failed");
          this.authReject?.(err);
          this.cleanupAuth();
          this.onAuthFailure?.(err);
          this.closeOnAuthFailure?.();
          break;
        }
        this.channel.dispatch(frame);
        break;
      }
      default:
        this.channel.dispatch(frame);
    }
  }

  private checkTransId(transId: bigint): boolean {
    if (transId === 0n) return true;
    if (this.lastTransId === null) {
      this.lastTransId = transId;
      return true;
    }
    const expected = this.lastTransId + 1n;
    if (transId === expected) {
      this.lastTransId = transId;
      return true;
    }
    const kind: TransIdGapKind = transId < expected ? "duplicate" : "skip";
    const verdict = this.onTransIdGap?.(kind, transId, expected) ?? "close";
    if (verdict === "accept") {
      this.lastTransId = transId;
      return true;
    }
    return false;
  }

  /* ------------------------------------------------------------------ */
  /* Payload encode / decode (binary codec)                             */
  /* ------------------------------------------------------------------ */

  /**
   * Encode an application payload into Uint8Array + encoding flag.
   * Used by the transport when building a binary wire frame.
   */
  encodePayload(
    payload: unknown,
    reqSchemaId?: number,
    namespace?: string,
    context?: { callID?: string; target?: string; frameType?: string },
  ): { bytes: Uint8Array; encoding: PayloadEncoding } {
    if (payload === undefined) return { bytes: new Uint8Array(0), encoding: PayloadEncoding.Binary };

    if (this.binaryCodec && reqSchemaId !== undefined) {
      const sys = this.encodeSystemPayload(payload, reqSchemaId);
      if (sys) return sys;
    }

    if (
      reqSchemaId !== undefined &&
      this.schemaRegistry &&
      this.binaryCodec
    ) {
      let entry: SchemaEntry | undefined;
      let objectFor: ((name: string) => ObjectDesc | undefined) | undefined;
      if (namespace) {
        entry = this.schemaRegistry.get(namespace, reqSchemaId);
        if (entry) objectFor = this.schemaRegistry.objectFor(namespace);
      }
      if (!entry) {
        entry = this.schemaRegistry.getById(reqSchemaId);
        if (entry) objectFor = this.schemaRegistry.objectFor(entry.namespace);
      }
      if (entry && objectFor) {
        const encoded = this.binaryCodec.encode(entry.type, payload, objectFor);
        return { bytes: encoded, encoding: PayloadEncoding.Binary };
      }
      if (this.binaryOnly) {
        throw new Error(
          `binary-only: missing schema entry${this.contextStr(context)}; namespace=${namespace ?? "undefined"}, reqSchemaId=${reqSchemaId}, payloadType=${this.describePayload(payload)}`
        );
      }
    }

    if (this.binaryOnly) {
      throw new Error(
        `binary-only: cannot encode struct payload${this.contextStr(context)}; ` +
        `reqSchemaId=${reqSchemaId ?? "undefined"}, namespace=${namespace ?? "undefined"}, ` +
        `hasSchemaRegistry=${!!this.schemaRegistry}, hasBinaryCodec=${!!this.binaryCodec}, ` +
        `payloadType=${this.describePayload(payload)}`
      );
    }

    const json = JSON.stringify(payload);
    return { bytes: new TextEncoder().encode(json), encoding: PayloadEncoding.JSON };
  }

  private contextStr(context?: { callID?: string; target?: string; frameType?: string }): string {
    if (!context?.callID) return "";
    let s = ` for ${context.callID}`;
    if (context.target) s += ` target=${context.target}`;
    return s;
  }

  private describePayload(payload: unknown): string {
    if (payload === null) return "null";
    const t = typeof payload;
    if (t !== "object") return t;
    if (payload instanceof Uint8Array) return "Uint8Array";
    if (payload instanceof ArrayBuffer) return "ArrayBuffer";
    if (Array.isArray(payload)) return `array[${payload.length}]`;
    try {
      const keys = Object.keys(payload as Record<string, unknown>);
      const head = keys.slice(0, 5).join(",");
      return `object{${head}${keys.length > 5 ? ",..." : ""}}`;
    } catch {
      return "object";
    }
  }

  private encodeSystemPayload(
    payload: unknown,
    reqSchemaId: number,
  ): { bytes: Uint8Array; encoding: PayloadEncoding } | undefined {
    switch (reqSchemaId) {
      case SYS_EVENT_SUBSCRIBE_SERVICE_REQ: {
        const of = sysObjectFor(SYS_EVENT_SUBSCRIBE_SERVICE_REQ);
        const encoded = this.binaryCodec!.encode(EVENT_SUBSCRIBE_SERVICE_REQ_DESC, payload, of);
        return { bytes: encoded, encoding: PayloadEncoding.Binary };
      }
      case SYS_EVENT_SUBSCRIBE_INSTANCE_REQ: {
        const of = sysObjectFor(SYS_EVENT_SUBSCRIBE_INSTANCE_REQ);
        const encoded = this.binaryCodec!.encode(EVENT_SUBSCRIBE_INSTANCE_REQ_DESC, payload, of);
        return { bytes: encoded, encoding: PayloadEncoding.Binary };
      }
      case SYS_PROJECTION_GET_REQ: {
        const of = sysObjectFor(SYS_PROJECTION_GET_REQ);
        const encoded = this.binaryCodec!.encode(PROJECTION_GET_REQ_DESC, payload, of);
        return { bytes: encoded, encoding: PayloadEncoding.Binary };
      }
      case SYS_PROJECTION_WATCH_REQ: {
        const of = sysObjectFor(SYS_PROJECTION_WATCH_REQ);
        const encoded = this.binaryCodec!.encode(PROJECTION_WATCH_REQ_DESC, payload, of);
        return { bytes: encoded, encoding: PayloadEncoding.Binary };
      }
      default:
        return undefined;
    }
  }

  /** Encode an AuthReq{Token: token}. */
  private encodeAuthReq(token: string): string | Uint8Array {
    if (!this.binaryCodec) {
      return new TextEncoder().encode(token);
    }
    const val = { Token: token };
    const of = sysObjectFor(SYS_AUTH_REQ);
    return this.binaryCodec.encode(AUTH_REQ_DESC, val, of);
  }

  /** Decode payload bytes using registered schema info. */
  private decodePayload(
    payloadBytes: Uint8Array,
    enc: PayloadEncoding,
    reqId?: number,
    subId?: string,
  ): unknown {
    if (payloadBytes.length === 0) return undefined;

    if (enc === PayloadEncoding.JSON) {
      if (this.binaryOnly) {
        throw new Error("binary-only: received JSON-encoded payload");
      }
      try {
        return JSON.parse(new TextDecoder().decode(payloadBytes));
      } catch {
        return payloadBytes;
      }
    }

    if (enc === PayloadEncoding.Binary && this.schemaRegistry && this.binaryCodec) {
      let schemaId: number | undefined;
      let namespace: string | undefined;

      if (reqId !== undefined) {
        const info = this.pendingResInfo.get(reqId);
        if (info) {
          schemaId = info.resSchemaId;
          namespace = info.namespace;
        }
      } else if (subId) {
        const info = this.subChunkInfo.get(subId);
        if (info) {
          schemaId = info.chunkSchemaId;
          namespace = info.namespace;
        }
      }

      if (schemaId !== undefined) {
        let entry: SchemaEntry | undefined;
        let objectFor: ((name: string) => ObjectDesc | undefined) | undefined;
        if (namespace) {
          entry = this.schemaRegistry.get(namespace, schemaId);
          if (entry) objectFor = this.schemaRegistry.objectFor(namespace);
        }
        if (!entry) {
          entry = this.schemaRegistry.getById(schemaId);
          if (entry) objectFor = this.schemaRegistry.objectFor(entry.namespace);
        }
        if (entry && objectFor) {
          try {
            return this.binaryCodec.decode(entry.type, payloadBytes, objectFor);
          } catch (err) {
            console.warn(`ws: binary decode failed for ${namespace ?? "(none)"}/${schemaId}:`, err);
          }
        }
      }

      // Self-describing fallback.
      if (isTBCPayload(payloadBytes)) {
        const tbcId = tbcStructSchemaId(payloadBytes);
        if (tbcId > 0) {
          const entry = this.schemaRegistry.getById(tbcId);
          if (entry) {
            try {
              const objectFor = this.schemaRegistry.objectFor(entry.namespace);
              return this.binaryCodec.decode(entry.type, payloadBytes, objectFor);
            } catch (err) {
              console.warn(`ws: self-describing decode failed for schema ${tbcId}:`, err);
            }
          }
        }
        try {
          return this.binaryCodec.decode({ kind: "scalar", name: "any" }, payloadBytes);
        } catch (err) {
          console.warn("ws: anonymous TBC decode failed:", err);
        }
      }
    }

    return payloadBytes;
  }

  /* ------------------------------------------------------------------ */
  /* AuthOk → Manifest → schema cache                                   */
  /* ------------------------------------------------------------------ */

  private processAuthOk(payload: unknown): void {
    if (!this.binaryCodec || !payload) return;

    let manifestStr: string | undefined;

    if (this.binaryOnly) {
      if (!(payload instanceof Uint8Array)) return;
      try {
        const of = sysObjectFor(SYS_AUTH_OK);
        const decoded = this.binaryCodec.decode(AUTH_OK_DESC, payload, of);
        if (typeof decoded === "object" && decoded !== null && "Manifest" in decoded) {
          manifestStr = (decoded as { Manifest: string }).Manifest;
        }
      } catch (err) {
        console.warn("ws: binary-only auth_ok decode failed:", err);
      }
    } else {
      if (typeof payload === "object" && payload !== null && "Manifest" in payload) {
        manifestStr = (payload as { Manifest: string }).Manifest;
      } else if (payload instanceof Uint8Array) {
        try {
          const of = sysObjectFor(SYS_AUTH_OK);
          const decoded = this.binaryCodec.decode(AUTH_OK_DESC, payload, of);
          if (typeof decoded === "object" && decoded !== null && "Manifest" in decoded) {
            manifestStr = (decoded as { Manifest: string }).Manifest;
          }
        } catch {
          // Not TBC-encoded, ignore.
        }
      }
    }

    if (!manifestStr) return;

    try {
      const manifest = JSON.parse(manifestStr);
      this.buildCallableCache(manifest);
    } catch (e) {
      console.warn("ws: failed to parse auth_ok manifest:", e);
    }
  }

  private buildCallableCache(manifest: {
    callables?: Array<{
      namespace: string;
      name: string;
      mode: string;
      reqSchemaId: number;
      finalSchemaId: number;
      chunkSchemaId?: number;
    }>;
  }): void {
    if (!manifest.callables) return;
    for (const c of manifest.callables) {
      const callID = c.namespace ? `${c.namespace}.${c.name}` : c.name;
      this.callableSchemaCache.set(callID, {
        reqSchemaId: c.reqSchemaId,
        finalSchemaId: c.finalSchemaId,
        chunkSchemaId: c.chunkSchemaId,
        namespace: c.namespace,
        mode: c.mode,
      });
    }
  }

  private resolveCallableSchema(callID: string): CallableSchemaMeta | undefined {
    return this.callableSchemaCache.get(callID);
  }

  private resolveReqSchemaId(callID: string, opts?: InvokeOptions): number | undefined {
    if (opts?.reqSchemaId !== undefined) return opts.reqSchemaId;
    const cached = this.resolveCallableSchema(callID);
    if (cached?.reqSchemaId !== undefined) return cached.reqSchemaId;
    if (callID === "gospore.events.subscribe_service") return SYS_EVENT_SUBSCRIBE_SERVICE_REQ;
    if (callID === "gospore.events.subscribe_instance") return SYS_EVENT_SUBSCRIBE_INSTANCE_REQ;
    if (callID === "gospore.projection.get") return SYS_PROJECTION_GET_REQ;
    if (callID === "gospore.projection.watch") return SYS_PROJECTION_WATCH_REQ;
    return undefined;
  }

  /* ------------------------------------------------------------------ */
  /* Subscription resume                                                */
  /* ------------------------------------------------------------------ */

  /** Re-send subscribe frames for all active subscriptions after reconnect. */
  resumeSubscriptions(): void {
    const resumeAll = () => {
      for (const [subId, sub] of this.channel.subscriptions) {
        sub.error = null;
        sub.done = false;
        const cached = this.resolveCallableSchema(sub.callID);
        const namespace = cached?.namespace ?? sub.callID.substring(0, sub.callID.lastIndexOf(".")) ?? sub.callID;
        const frame: SubscribeFrame = {
          type: "subscribe",
          subId,
          callID: sub.callID,
          payload: sub.payload,
          sinceSeqNo: sub.sinceSeqNo,
        };
        if (sub.target) frame.target = sub.target;
        frame.reqSchemaId = this.resolveReqSchemaId(sub.callID, { reqSchemaId: sub.reqSchemaId });
        frame.namespace = namespace;
        if (!this.channel.send || !this.channel.send(frame)) {
          sub.error = new Error("websocket not connected");
          sub.done = true;
          for (const it of sub.iterators) it.notify();
        }
      }
    };

    if (this.authPromise) {
      this.authPromise.then(resumeAll).catch(() => {});
    } else {
      resumeAll();
    }
  }
}
