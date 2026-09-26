/**
 * FrameChannel — low-level frame correlation and subscription dispatch.
 *
 * Owns pending-invoke correlation (reqId → resolve/reject) and
 * subscription dispatch (subId → iterators/queue).  Has no knowledge
 * of binary encoding, auth, or WebSocket — it works purely with
 * application-level frame objects.
 */

/* ------------------------------------------------------------------ */
/* Frame types (application-level, before binary serialisation)       */
/* ------------------------------------------------------------------ */

export interface InvokeFrame {
  type: "invoke";
  reqId: number;
  callID: string;
  target?: string;
  payload: unknown;
  reqSchemaId?: number;
  namespace?: string;
  timeoutMs?: number;
}

export interface ReplyFrame {
  type: "reply";
  reqId: number;
  payload: unknown;
}

export interface ErrorFrame {
  type: "error";
  reqId?: number;
  subId?: string;
  message?: string;
}

export interface SubscribeFrame {
  type: "subscribe";
  subId: string;
  callID: string;
  target?: string;
  payload: unknown;
  sinceSeqNo: number;
  reqSchemaId?: number;
  namespace?: string;
}

export interface UnsubscribeFrame {
  type: "unsubscribe";
  subId: string;
}

export interface ChunkFrame {
  type: "chunk";
  subId: string;
  seqNo: number;
  payload: unknown;
}

export interface EndFrame {
  type: "end";
  subId: string;
}

export interface AuthFrame {
  type: "auth";
  payload: string | Uint8Array;
}

export interface AuthOkFrame {
  type: "auth_ok";
  payload?: unknown;
}

export interface PingFrame {
  type: "ping";
}

export interface PongFrame {
  type: "pong";
}

export type WireFrame =
  | InvokeFrame
  | ReplyFrame
  | ErrorFrame
  | SubscribeFrame
  | UnsubscribeFrame
  | ChunkFrame
  | EndFrame
  | AuthFrame
  | AuthOkFrame
  | PingFrame
  | PongFrame;

/* ------------------------------------------------------------------ */
/* WithSeqNo wrapper type                                             */
/* ------------------------------------------------------------------ */

/** A chunk yielded with its sequence number. */
export interface WithSeqNo<T> {
  seqNo: number;
  payload: T;
}

/* ------------------------------------------------------------------ */
/* Pending request / active subscription internals                    */
/* ------------------------------------------------------------------ */

interface PendingRequest {
  callID: string;
  frame: InvokeFrame;
  resolve: (value: unknown) => void;
  reject: (reason: unknown) => void;
  timer: ReturnType<typeof setTimeout>;
}

interface ActiveSubscription {
  callID: string;
  payload: unknown;
  target?: string;
  reqSchemaId?: number;
  sinceSeqNo: number;
  withSeqNo: boolean;
  queue: Array<{seqNo: number; payload: unknown}>;
  error: Error | null;
  done: boolean;
  iterators: Set<SubscriptionIterator>;
}

export class SubscriptionIterator implements AsyncIterator<unknown> {
  private sub: ActiveSubscription;
  private index: number = 0;
  private disposed: boolean = false;
  private onDispose: () => void;
  private waitResolve: (() => void) | null = null;

  constructor(sub: ActiveSubscription, onDispose: () => void) {
    this.sub = sub;
    this.onDispose = onDispose;
    sub.iterators.add(this);
  }

  async next(): Promise<IteratorResult<unknown>> {
    if (this.disposed) {
      return { value: undefined, done: true };
    }

    while (true) {
      if (this.index < this.sub.queue.length) {
        const entry = this.sub.queue[this.index++];
        compactQueue(this.sub);
        if (!entry) continue;
        const value = this.sub.withSeqNo
          ? { seqNo: entry.seqNo, payload: entry.payload }
          : entry.payload;
        return { value, done: false };
      }
      if (this.sub.error) throw this.sub.error;
      if (this.sub.done) return { value: undefined, done: true };
      // A parallel return()/dispose() may have run while we were waiting;
      // re-check before re-arming the waiter so the read loop terminates
      // instead of parking on a promise that nothing will ever resolve.
      if (this.disposed) return { value: undefined, done: true };

      await new Promise<void>((resolve) => {
        this.waitResolve = resolve;
      });
      this.waitResolve = null;
    }
  }

  return(): Promise<IteratorResult<unknown>> {
    this.dispose();
    return Promise.resolve({ value: undefined, done: true });
  }

  [Symbol.asyncIterator](): AsyncIterator<unknown> {
    return this;
  }

  /** Wakes the iterator so it re-checks the queue. */
  notify(): void {
    this.waitResolve?.();
  }

  /** Current queue position. Package-internal, used by compactQueue. */
  get queueIndex(): number {
    return this.index;
  }

  /** Shift the queue position back by n after the queue head was dropped.
   * Package-internal, used by compactQueue. */
  rewind(n: number): void {
    this.index -= n;
  }

  private dispose(): void {
    if (this.disposed) return;
    this.disposed = true;
    this.sub.iterators.delete(this);
    this.waitResolve?.();
    this.onDispose();
  }
}

/* ------------------------------------------------------------------ */
/* Consumed-prefix compaction                                         */
/* ------------------------------------------------------------------ */

/**
 * Drop the head of a subscription queue once every live iterator has consumed
 * it. Without this the queue retains every chunk payload for the whole
 * lifetime of the subscription: a long-lived stream (e.g. agent_list_state at
 * ~100KB per event, one event per second) grows the JS heap without bound.
 * Compaction is thresholded so the O(len) splice amortizes across many
 * consumed chunks.
 */
const QUEUE_COMPACT_MIN = 32;

function compactQueue(sub: ActiveSubscription): void {
  if (sub.iterators.size === 0 || sub.queue.length === 0) return;
  let min = Infinity;
  for (const it of sub.iterators) {
    if (it.queueIndex < min) min = it.queueIndex;
  }
  if (min < QUEUE_COMPACT_MIN) return;
  sub.queue.splice(0, min);
  for (const it of sub.iterators) it.rewind(min);
}

/* ------------------------------------------------------------------ */
/* FrameChannel                                                       */
/* ------------------------------------------------------------------ */

export interface QueuedInvoke {
  frame: InvokeFrame;
  resolve: (value: unknown) => void;
  reject: (reason: unknown) => void;
  timer: ReturnType<typeof setTimeout>;
}

/**
 * FrameChannel correlates outgoing requests with incoming replies and
 * dispatches subscription data to active iterators.
 *
 * It does NOT encode/decode payloads or handle auth — that's the
 * responsibility of WireSession (or a similar higher-level wrapper).
 */
export class FrameChannel {
  nextReqId = 1;
  pending = new Map<number, PendingRequest>();
  subscriptions = new Map<string, ActiveSubscription>();
  invokeQueue: QueuedInvoke[] = [];

  /** Send an application-level frame. Returns true if the frame was
   *  handed to the transport.  Provided by the owner (WireSession /
   *  WebSocketTransport). */
  send: ((frame: WireFrame) => boolean | Promise<void>) | null = null;

  /** Called when a subscription's last iterator is disposed and the
   *  socket is open.  Provided by the owner so it can send an
   *  unsubscribe frame. */
  sendUnsubscribe: ((subId: string) => void) | null = null;

  constructor() {}

  /**
   * Create a pending request entry and return a Promise that resolves
   * with the reply payload or rejects on timeout / error.
   */
  sendInvoke(
    frame: InvokeFrame,
    timeoutMs: number,
    resolve: (value: unknown) => void,
    reject: (reason: unknown) => void,
  ): void {
    const timer = setTimeout(() => {
      if (this.pending.has(frame.reqId)) {
        this.pending.delete(frame.reqId);
        reject(new Error(`invoke ${frame.callID} timed out`));
      }
    }, timeoutMs);

    this.pending.set(frame.reqId, {
      callID: frame.callID,
      frame,
      resolve: (value) => { clearTimeout(timer); resolve(value); },
      reject: (reason) => { clearTimeout(timer); reject(reason); },
      timer,
    });

    if (!this.send || !this.send(frame)) {
      this.pending.delete(frame.reqId);
      clearTimeout(timer);
      reject(new Error("websocket not connected"));
    }
  }

  /** Queue an invoke while the connection is not yet open. */
  queueInvoke(
    frame: InvokeFrame,
    resolve: (value: unknown) => void,
    reject: (reason: unknown) => void,
    timeoutMs: number,
  ): void {
    const timer = setTimeout(() => {
      const idx = this.invokeQueue.findIndex((q) => q.frame.reqId === frame.reqId);
      if (idx >= 0) {
        this.invokeQueue.splice(idx, 1);
        reject(new Error(`invoke ${frame.callID} timed out while connecting`));
      }
    }, timeoutMs);
    this.invokeQueue.push({ frame, resolve, reject, timer });
  }

  /**
   * Create a subscription entry and return an object with an async
   * iterator symbol.
   */
  createSubscription(subId: string, sub: ActiveSubscription): { [Symbol.asyncIterator]: () => AsyncIterator<unknown> } {
    this.subscriptions.set(subId, sub);

    const onDispose = () => {
      if (sub.iterators.size === 0) {
        if (this.sendUnsubscribe) {
          this.sendUnsubscribe(subId);
        }
        this.subscriptions.delete(subId);
      }
    };

    return {
      [Symbol.asyncIterator]: () => new SubscriptionIterator(sub, onDispose),
    };
  }

  /** Route an incoming application-level frame to the right handler. */
  dispatch(frame: WireFrame): void {
    switch (frame.type) {
      case "reply": {
        const pending = this.pending.get(frame.reqId);
        if (pending) {
          this.pending.delete(frame.reqId);
          pending.resolve(frame.payload);
        }
        break;
      }
      case "error": {
        if (frame.reqId) {
          const pending = this.pending.get(frame.reqId);
          if (pending) {
            this.pending.delete(frame.reqId);
            pending.reject(new Error(frame.message ?? "unknown error"));
            break;
          }
        }
        if (frame.subId) {
          const sub = this.subscriptions.get(frame.subId);
          if (sub) {
            sub.error = new Error(frame.message ?? "unknown error");
            sub.done = true;
            for (const it of sub.iterators) it.notify();
            this.subscriptions.delete(frame.subId);
          }
        }
        break;
      }
      case "chunk": {
        const sub = this.subscriptions.get(frame.subId);
        if (sub) {
          sub.queue.push({ seqNo: frame.seqNo, payload: frame.payload });
          sub.sinceSeqNo = frame.seqNo;
          for (const it of sub.iterators) it.notify();
        }
        break;
      }
      case "end": {
        const sub = this.subscriptions.get(frame.subId);
        if (sub) {
          sub.done = true;
          for (const it of sub.iterators) it.notify();
        }
        break;
      }
      default:
        // Other frame types (auth_ok, pong, ping) are handled by the session/transport.
        break;
    }
  }

  /** Reject all pending and move pending requests to invokeQueue for
   *  retry after reconnect.  Returns the captured items. */
  capturePending(): QueuedInvoke[] {
    const captured: QueuedInvoke[] = [];
    for (const pending of this.pending.values()) {
      clearTimeout(pending.timer);
      captured.push({
        frame: pending.frame,
        resolve: pending.resolve,
        reject: pending.reject,
        timer: setTimeout(() => {}), // placeholder
      });
    }
    this.pending.clear();
    return captured;
  }

  /** Close all pending requests and subscriptions with an error. */
  close(error: Error): void {
    for (const q of this.invokeQueue) {
      clearTimeout(q.timer);
      q.reject(error);
    }
    this.invokeQueue = [];

    for (const pending of this.pending.values()) {
      pending.reject(error);
    }
    this.pending.clear();

    for (const sub of this.subscriptions.values()) {
      sub.done = true;
      sub.error = error;
      for (const it of sub.iterators) it.notify();
    }
    this.subscriptions.clear();
  }

  /** Notify all subscription iterators (e.g. after reconnect). */
  notifySubscriptions(): void {
    for (const sub of this.subscriptions.values()) {
      for (const it of sub.iterators) it.notify();
    }
  }
}
