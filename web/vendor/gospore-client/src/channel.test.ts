import { describe, it, expect, vi } from "vitest";
import { FrameChannel, SubscriptionIterator } from "./channel";
import type { InvokeFrame } from "./channel";

/* ------------------------------------------------------------------ */

describe("FrameChannel", () => {
  it("resolves pending request on reply", async () => {
    const sent: unknown[] = [];
    const ch = new FrameChannel();
    ch.send = (frame) => { sent.push(frame); return true; };

    const reqId = ch.nextReqId++;
    const frame: InvokeFrame = { type: "invoke", reqId, callID: "test.foo", payload: { x: 1 } };

    const promise = new Promise<unknown>((resolve, reject) => {
      ch.sendInvoke(frame, 5000, resolve, reject);
    });

    expect(sent.length).toBe(1);

    ch.dispatch({ type: "reply", reqId, payload: { result: "ok" } });
    await expect(promise).resolves.toEqual({ result: "ok" });
  });

  it("rejects pending request on error frame", async () => {
    const ch = new FrameChannel();
    ch.send = () => true;

    const reqId = ch.nextReqId++;
    const frame: InvokeFrame = { type: "invoke", reqId, callID: "test.bar", payload: {} };

    const promise = new Promise<unknown>((resolve, reject) => {
      ch.sendInvoke(frame, 5000, resolve, reject);
    });

    ch.dispatch({ type: "error", reqId, message: "something went wrong" });
    await expect(promise).rejects.toThrow("something went wrong");
  });

  it("rejects pending on timeout", async () => {
    vi.useFakeTimers();
    const ch = new FrameChannel();
    ch.send = () => true;

    const reqId = ch.nextReqId++;
    const frame: InvokeFrame = { type: "invoke", reqId, callID: "test.timeout", payload: {} };

    const promise = new Promise<unknown>((resolve, reject) => {
      ch.sendInvoke(frame, 100, resolve, reject);
    });

    vi.advanceTimersByTime(150);
    await expect(promise).rejects.toThrow("test.timeout timed out");
    vi.useRealTimers();
  });

  it("rejects pending on error when send fails", async () => {
    const ch = new FrameChannel();
    ch.send = () => false;

    const reqId = ch.nextReqId++;
    const frame: InvokeFrame = { type: "invoke", reqId, callID: "test.sendfail", payload: {} };

    await expect(
      new Promise<unknown>((resolve, reject) => {
        ch.sendInvoke(frame, 5000, resolve, reject);
      }),
    ).rejects.toThrow("websocket not connected");
  });

  it("queues and flushes invoke via queueInvoke", async () => {
    vi.useFakeTimers();
    const ch = new FrameChannel();
    ch.send = () => true;

    const reqId = ch.nextReqId++;
    const frame: InvokeFrame = { type: "invoke", reqId, callID: "test.queue", payload: {} };

    const promise = new Promise<unknown>((resolve, reject) => {
      ch.queueInvoke(frame, resolve, reject, 5000);
    });

    expect(ch.invokeQueue.length).toBe(1);

    // Move from queue to pending (simulate flush)
    const q = ch.invokeQueue.shift()!;
    ch.pending.set(reqId, {
      callID: frame.callID,
      frame,
      resolve: q.resolve,
      reject: q.reject,
      timer: q.timer,
    });

    ch.dispatch({ type: "reply", reqId, payload: "flushed" });
    await expect(promise).resolves.toBe("flushed");
    vi.useRealTimers();
  });

  it("delivers chunks to subscription iterator", async () => {
    const ch = new FrameChannel();
    ch.send = () => true;

    const subId = "sub:1";
    const sub = {
      callID: "test.stream",
      payload: {},
      sinceSeqNo: 0,
      withSeqNo: false,
      queue: [] as Array<{seqNo: number; payload: unknown}>,
      error: null as Error | null,
      done: false,
      iterators: new Set<SubscriptionIterator>(),
    };

    const iterable = ch.createSubscription(subId, sub);
    const iter = iterable[Symbol.asyncIterator]();

    ch.dispatch({ type: "chunk", subId, seqNo: 1, payload: "a" });
    ch.dispatch({ type: "chunk", subId, seqNo: 2, payload: "b" });

    const v1 = await iter.next();
    expect(v1.value).toBe("a");
    expect(v1.done).toBe(false);

    const v2 = await iter.next();
    expect(v2.value).toBe("b");
    expect(v2.done).toBe(false);

    await iter.return?.();
  });

  it("ends subscription on end frame", async () => {
    const ch = new FrameChannel();
    ch.send = () => true;

    const subId = "sub:end";
    const sub = {
      callID: "test.end",
      payload: {},
      sinceSeqNo: 0,
      withSeqNo: false,
      queue: [] as Array<{seqNo: number; payload: unknown}>,
      error: null as Error | null,
      done: false,
      iterators: new Set<SubscriptionIterator>(),
    };

    const iterable = ch.createSubscription(subId, sub);
    const iter = iterable[Symbol.asyncIterator]();

    ch.dispatch({ type: "chunk", subId, seqNo: 1, payload: "x" });
    expect((await iter.next()).value).toBe("x");

    ch.dispatch({ type: "end", subId });
    const final = await iter.next();
    expect(final.done).toBe(true);

    await iter.return?.();
  });

  it("throws on subscription error frame", async () => {
    const ch = new FrameChannel();
    ch.send = () => true;

    const subId = "sub:err";
    const sub = {
      callID: "test.err",
      payload: {},
      sinceSeqNo: 0,
      withSeqNo: false,
      queue: [] as Array<{seqNo: number; payload: unknown}>,
      error: null as Error | null,
      done: false,
      iterators: new Set<SubscriptionIterator>(),
    };

    const iterable = ch.createSubscription(subId, sub);
    const iter = iterable[Symbol.asyncIterator]();

    ch.dispatch({ type: "error", subId, message: "stream failed" });
    await expect(iter.next()).rejects.toThrow("stream failed");

    await iter.return?.();
  });

  it("captures pending and clears pending map", () => {
    const ch = new FrameChannel();
    ch.send = () => true;

    const reqId = ch.nextReqId++;
    const frame: InvokeFrame = { type: "invoke", reqId, callID: "test.cap", payload: {} };

    new Promise<unknown>((resolve, reject) => {
      ch.sendInvoke(frame, 5000, resolve, reject);
    });

    expect(ch.pending.size).toBe(1);

    const captured = ch.capturePending();
    expect(captured.length).toBe(1);
    expect(ch.pending.size).toBe(0);
    expect(captured[0]!.frame.callID).toBe("test.cap");
  });

  it("close rejects all pending and ends all subscriptions", () => {
    const ch = new FrameChannel();
    ch.send = () => true;

    const reqId = ch.nextReqId++;
    const frame: InvokeFrame = { type: "invoke", reqId, callID: "test.close", payload: {} };
    const promise = new Promise<unknown>((resolve, reject) => {
      ch.sendInvoke(frame, 5000, resolve, reject);
    });

    const subId = "sub:close";
    const sub = {
      callID: "test.sub.close",
      payload: {},
      sinceSeqNo: 0,
      withSeqNo: false,
      queue: [] as Array<{seqNo: number; payload: unknown}>,
      error: null as Error | null,
      done: false,
      iterators: new Set<SubscriptionIterator>(),
    };
    ch.createSubscription(subId, sub);

    ch.close(new Error("transport closed"));

    expect(promise).rejects.toThrow("transport closed");
    expect(ch.pending.size).toBe(0);
    expect(ch.subscriptions.size).toBe(0);
  });

  it("yields {seqNo, payload} when withSeqNo is true", async () => {
    const ch = new FrameChannel();
    ch.send = () => true;

    const subId = "sub:seqno";
    const sub = {
      callID: "test.withseqno",
      payload: {},
      sinceSeqNo: 0,
      withSeqNo: true,
      queue: [] as Array<{seqNo: number; payload: unknown}>,
      error: null as Error | null,
      done: false,
      iterators: new Set<SubscriptionIterator>(),
    };

    const iterable = ch.createSubscription(subId, sub);
    const iter = iterable[Symbol.asyncIterator]();

    ch.dispatch({ type: "chunk", subId, seqNo: 42, payload: "hello" });
    ch.dispatch({ type: "chunk", subId, seqNo: 99, payload: "world" });

    const v1 = await iter.next();
    expect(v1.done).toBe(false);
    expect(v1.value).toEqual({ seqNo: 42, payload: "hello" });

    const v2 = await iter.next();
    expect(v2.done).toBe(false);
    expect(v2.value).toEqual({ seqNo: 99, payload: "world" });

    await iter.return?.();
  });

  it("sends unsubscribe via sendUnsubscribe on last iterator dispose", async () => {
    const unsubFrames: string[] = [];
    const ch = new FrameChannel();
    ch.send = () => true;
    ch.sendUnsubscribe = (subId) => { unsubFrames.push(subId); };

    const subId = "sub:unsub";
    const sub = {
      callID: "test.unsub",
      payload: {},
      sinceSeqNo: 0,
      withSeqNo: false,
      queue: [] as Array<{seqNo: number; payload: unknown}>,
      error: null as Error | null,
      done: false,
      iterators: new Set<SubscriptionIterator>(),
    };

    const iterable = ch.createSubscription(subId, sub);
    const iter = iterable[Symbol.asyncIterator]();

    await iter.return?.();

    expect(unsubFrames).toContain(subId);
    expect(ch.subscriptions.size).toBe(0);
  });

  it("drops the consumed queue prefix so chunk payloads are not retained forever", async () => {
    const ch = new FrameChannel();
    ch.send = () => true;

    const subId = "sub:compact";
    const sub = {
      callID: "test.compact",
      payload: {},
      sinceSeqNo: 0,
      withSeqNo: false,
      queue: [] as Array<{seqNo: number; payload: unknown}>,
      error: null as Error | null,
      done: false,
      iterators: new Set<SubscriptionIterator>(),
    };

    const iterable = ch.createSubscription(subId, sub);
    const iter = iterable[Symbol.asyncIterator]();

    const N = 1000;
    for (let i = 1; i <= N; i++) {
      ch.dispatch({ type: "chunk", subId, seqNo: i, payload: { blob: "x".repeat(1024) } });
    }
    for (let i = 1; i <= N; i++) {
      const v = await iter.next();
      expect(v.value).toEqual({ blob: "x".repeat(1024) });
    }

    // All chunks consumed: the queue must not retain them.
    expect(sub.queue.length).toBeLessThanOrEqual(32);
  });

  it("keeps queue entries a slow parallel iterator has not consumed yet", async () => {
    const ch = new FrameChannel();
    ch.send = () => true;

    const subId = "sub:compact-multi";
    const sub = {
      callID: "test.compact-multi",
      payload: {},
      sinceSeqNo: 0,
      withSeqNo: false,
      queue: [] as Array<{seqNo: number; payload: unknown}>,
      error: null as Error | null,
      done: false,
      iterators: new Set<SubscriptionIterator>(),
    };

    const iterable = ch.createSubscription(subId, sub);
    const fast = iterable[Symbol.asyncIterator]() as SubscriptionIterator;
    const slow = iterable[Symbol.asyncIterator]() as SubscriptionIterator;

    const N = 100;
    for (let i = 1; i <= N; i++) {
      ch.dispatch({ type: "chunk", subId, seqNo: i, payload: i });
    }
    for (let i = 1; i <= N; i++) {
      await fast.next();
    }
    // Slow iterator consumes only 40; entries 41..100 must stay queued.
    for (let i = 1; i <= 40; i++) {
      const v = await slow.next();
      expect(v.value).toBe(i);
    }

    // Slow stopped at 40: the last compaction ran when it hit 32 (dropped
    // 32 entries), so 68 remain — 8 dead prefix + 60 not yet consumed by slow.
    expect(sub.queue.length).toBe(68);
    expect(sub.queue[8]!.payload).toBe(41);
    for (let i = 0; i < 68; i++) {
      expect(sub.queue[i]!.payload).toBe(33 + i);
    }
  });
});
