import { describe, it, expect } from "vitest";
import { WireSession } from "./session";
import { FrameType, PayloadEncoding, PayloadCompression, makeFlags } from "./binary_frame";
import type { WireFrame as BinaryWireFrame } from "./binary_frame";

/* ------------------------------------------------------------------ */

const TBC_MAGIC = new Uint8Array([0x54, 0x42, 0x43, 0x03]); // preamble + current wire version

function mockEncode(value: unknown): Uint8Array {
  const body = new TextEncoder().encode(JSON.stringify(value));
  const out = new Uint8Array(TBC_MAGIC.length + body.length);
  out.set(TBC_MAGIC, 0);
  out.set(body, TBC_MAGIC.length);
  return out;
}

function mockDecode(data: Uint8Array): unknown {
  if (data.length < TBC_MAGIC.length) throw new Error("not TBC");
  for (let i = 0; i < TBC_MAGIC.length; i++) {
    if (data[i] !== TBC_MAGIC[i]) throw new Error("not TBC");
  }
  return JSON.parse(new TextDecoder().decode(data.subarray(TBC_MAGIC.length)));
}

const mockBinaryCodec = {
  encode: (_desc: unknown, value: unknown) => mockEncode(value),
  decode: (_desc: unknown, data: Uint8Array) => mockDecode(data),
};

/* ------------------------------------------------------------------ */

describe("WireSession", () => {
  it("sends invoke frame and resolves on reply", async () => {
    const sent: unknown[] = [];
    const session = new WireSession({
      sendFrame: (frame) => { sent.push(frame); return true; },
      invokeTimeoutMs: 5000,
    });

    const promise = session.invoke("test.foo", { x: 1 });

    // The request was sent as an app-level InvokeFrame
    expect(sent.length).toBe(1);
    const f = sent[0] as any;
    expect(f.type).toBe("invoke");
    expect(f.callID).toBe("test.foo");

    // Simulate reply dispatch
    session.dispatchAppFrame({ type: "reply", reqId: f.reqId, payload: { result: "ok" } });
    await expect(promise).resolves.toEqual({ result: "ok" });
  });

  it("rejects invoke on error frame", async () => {
    const sent: unknown[] = [];
    const session = new WireSession({
      sendFrame: (frame) => { sent.push(frame); return true; },
      invokeTimeoutMs: 5000,
    });

    const promise = session.invoke("test.err", {});
    const f = sent[0] as any;

    session.dispatchAppFrame({ type: "error", reqId: f.reqId, message: "failure" });
    await expect(promise).rejects.toThrow("failure");
  });

  it("delivers chunks via subscribe", async () => {
    const sent: unknown[] = [];
    const session = new WireSession({
      sendFrame: (frame) => { sent.push(frame); return true; },
      invokeTimeoutMs: 5000,
    });

    const iterable = session.subscribe("test.stream", {});
    const iter = iterable[Symbol.asyncIterator]();

    const subFrame = sent.find((f: any) => f.type === "subscribe") as any;
    expect(subFrame).toBeDefined();
    expect(subFrame.callID).toBe("test.stream");

    session.dispatchAppFrame({ type: "chunk", subId: subFrame.subId, seqNo: 1, payload: "a" });
    const v1 = await iter.next();
    expect(v1.value).toBe("a");

    session.dispatchAppFrame({ type: "end", subId: subFrame.subId });
    const v2 = await iter.next();
    expect(v2.done).toBe(true);

    await iter.return?.();
  });

  it("rejects subscription on error", async () => {
    const sent: unknown[] = [];
    const session = new WireSession({
      sendFrame: (frame) => { sent.push(frame); return true; },
      invokeTimeoutMs: 5000,
    });

    const iterable = session.subscribe("test.serr", {});
    const iter = iterable[Symbol.asyncIterator]();
    const subFrame = sent.find((f: any) => f.type === "subscribe") as any;

    session.dispatchAppFrame({ type: "error", subId: subFrame.subId, message: "stream error" });
    await expect(iter.next()).rejects.toThrow("stream error");

    await iter.return?.();
  });

  it("sends unsubscribe on last iterator dispose", async () => {
    const sent: unknown[] = [];
    const session = new WireSession({
      sendFrame: (frame) => { sent.push(frame); return true; },
      invokeTimeoutMs: 5000,
    });

    const iterable = session.subscribe("test.unsub", {});
    const iter = iterable[Symbol.asyncIterator]();
    const subFrame = sent.find((f: any) => f.type === "subscribe") as any;

    await iter.return?.();

    const unsubFrame = sent.find((f: any) => f.type === "unsubscribe") as any;
    expect(unsubFrame).toBeDefined();
    expect(unsubFrame.subId).toBe(subFrame.subId);
  });

  it("does not send deferred subscribe when the iterator was disposed during auth", async () => {
    const sent: unknown[] = [];
    const session = new WireSession({
      sendFrame: (frame) => { sent.push(frame); return true; },
      invokeTimeoutMs: 5000,
    });

    // Auth is pending: subscribe() defers sendSub on authPromise.
    session.startAuth("token");

    const iterable = session.subscribe("test.authrace", {});
    const iter = iterable[Symbol.asyncIterator]();

    // No subscribe frame should have been sent yet.
    expect(sent.find((f: any) => f.type === "subscribe")).toBeUndefined();

    // Caller disposes before auth completes: unsubscribe goes out (a backend
    // no-op at this point) and the channel entry is removed.
    await iter.return?.();
    expect(sent.find((f: any) => f.type === "unsubscribe")).toBeDefined();

    // Auth completes -> the deferred sendSub must NOT create a backend
    // subscription nobody can ever cancel.
    session.dispatchAppFrame({ type: "auth_ok", payload: {} });

    // Let the authPromise.then(sendSub) microtask run.
    await new Promise<void>((r) => setTimeout(r, 0));

    expect(sent.find((f: any) => f.type === "subscribe")).toBeUndefined();
  });

  it("next() terminates when the iterator is disposed while waiting", async () => {
    const sent: unknown[] = [];
    const session = new WireSession({
      sendFrame: (frame) => { sent.push(frame); return true; },
      invokeTimeoutMs: 5000,
    });

    const iterable = session.subscribe("test.disposewake", {});
    const iter = iterable[Symbol.asyncIterator]();

    // Park next() on the waiter promise (no chunks, not done).
    const nextPromise = iter.next();
    await new Promise<void>((r) => setTimeout(r, 0));

    // Dispose wakes the parked next(); it must resolve done instead of
    // re-arming a waiter that nothing will ever resolve.
    await iter.return?.();
    const result = await nextPromise;
    expect(result.done).toBe(true);
  });

  it("handles auth handshake: auth frame sent, resolves on auth_ok", async () => {
    const sent: unknown[] = [];
    let authReadyFired = false;

    const session = new WireSession({
      sendFrame: (frame) => { sent.push(frame); return true; },
      onAuthReady: () => { authReadyFired = true; },
      invokeTimeoutMs: 5000,
    });

    // Start auth (as transport would after socket open)
    session.startAuth("my-token");

    const authFrame = sent.find((f: any) => f.type === "auth") as any;
    expect(authFrame).toBeDefined();

    // Send auth_ok
    expect(session.authHandshake).not.toBeNull();
    session.dispatchAppFrame({ type: "auth_ok", payload: { Manifest: '{"callables":[]}' } });

    await session.authHandshake;
    expect(authReadyFired).toBe(true);
  });

  it("rejects auth on error frame with no reqId", async () => {
    const sent: unknown[] = [];
    let authFailureFired = false;
    let authFailureErr: Error | null = null;

    const session = new WireSession({
      sendFrame: (frame) => { sent.push(frame); return true; },
      onAuthFailure: (err) => { authFailureFired = true; authFailureErr = err; },
      invokeTimeoutMs: 5000,
    });

    session.startAuth("bad-token");
    const authPromise = session.authHandshake; // capture before cleanup
    session.dispatchAppFrame({ type: "error", message: "unauthorized" });

    await expect(authPromise).rejects.toThrow("unauthorized");
    expect(authFailureFired).toBe(true);
    expect((authFailureErr as Error | null)?.message).toBe("unauthorized");
  });

  it("builds callable schema cache from auth_ok manifest", async () => {
    const sent: unknown[] = [];
    const session = new WireSession({
      sendFrame: (frame) => { sent.push(frame); return true; },
      binaryCodec: mockBinaryCodec,
      invokeTimeoutMs: 5000,
    });

    session.startAuth("token");

    const manifest = JSON.stringify({
      callables: [
        { namespace: "app", name: "greet", mode: "unary", reqSchemaId: 10, finalSchemaId: 20 },
        { namespace: "app", name: "events", mode: "stream", reqSchemaId: 11, finalSchemaId: 21, chunkSchemaId: 30 },
      ],
    });

    session.dispatchAppFrame({ type: "auth_ok", payload: { Manifest: manifest } });
    await session.authHandshake;

    // Invoke "app.greet" should resolve reqSchemaId from cache
    const promise = session.invoke("app.greet", { name: "world" });
    const invokeFrame = sent.find((f: any) => f.type === "invoke") as any;
    expect(invokeFrame.reqSchemaId).toBe(10);
    expect(invokeFrame.namespace).toBe("app");
    session.dispatchAppFrame({ type: "reply", reqId: invokeFrame.reqId, payload: "hello" });
    await expect(promise).resolves.toBe("hello");
  });

  it("rejects pending on close", async () => {
    const session = new WireSession({
      sendFrame: () => true,
      invokeTimeoutMs: 5000,
    });

    const promise = session.invoke("test.close", {});
    session.close();
    await expect(promise).rejects.toThrow("transport closed");
  });

  it("encodes payload with system builtin schema", () => {
    const session = new WireSession({
      sendFrame: () => true,
      binaryCodec: mockBinaryCodec,
      invokeTimeoutMs: 5000,
    });

    const result = session.encodePayload(
      { serviceName: "ws", kind: "events" },
      66, // SYS_EVENT_SUBSCRIBE_SERVICE_REQ
      "gospore",
    );
    expect(result.encoding).toBe(PayloadEncoding.Binary);
    expect(result.bytes.length).toBeGreaterThan(0);
  });

  it("encodes JSON payload when no binary codec", () => {
    const session = new WireSession({
      sendFrame: () => true,
      invokeTimeoutMs: 5000,
    });

    const result = session.encodePayload({ foo: "bar" });
    expect(result.encoding).toBe(PayloadEncoding.JSON);
    const decoded = JSON.parse(new TextDecoder().decode(result.bytes));
    expect(decoded).toEqual({ foo: "bar" });
  });

  it("receives binary wire frame and dispatches reply", async () => {
    const session = new WireSession({
      sendFrame: () => true,
      invokeTimeoutMs: 5000,
    });

    const promise = session.invoke("test.binary", {});

    // Build a binary reply frame
    const wire: BinaryWireFrame = {
      flags: makeFlags(PayloadEncoding.JSON, PayloadCompression.None),
      type: FrameType.Reply,
      corId: 1n,
      seq: 0,
      transId: 1n,
      callId: "",
      subId: "",
      target: "",
      from: "",
      errorMsg: "",
      payload: new TextEncoder().encode(JSON.stringify({ result: "bin" })),
      headers: new Map(),
    };

    const appFrame = session.receiveBinaryWireFrame(wire);
    expect(appFrame).not.toBeNull();
    expect(appFrame!.type).toBe("reply");
    expect((appFrame! as any).payload).toEqual({ result: "bin" });

    session.dispatchAppFrame(appFrame!);
    await expect(promise).resolves.toEqual({ result: "bin" });
  });

  describe("transId gap policy", () => {
    function replyFrame(transId: bigint, reqId: bigint): BinaryWireFrame {
      return {
        flags: makeFlags(PayloadEncoding.JSON, PayloadCompression.None),
        type: FrameType.Reply,
        corId: reqId,
        seq: 0,
        transId,
        callId: "",
        subId: "",
        target: "",
        from: "",
        errorMsg: "",
        payload: new TextEncoder().encode(JSON.stringify({ ok: true })),
        headers: new Map(),
      };
    }

    it("skip with accept verdict resyncs forward and processes the frame", async () => {
      const gaps: Array<[string, bigint, bigint]> = [];
      const session = new WireSession({
        sendFrame: () => true,
        invokeTimeoutMs: 60000,
        onTransIdGap: (kind, got, expected) => {
          gaps.push([kind, got, expected]);
          return "accept";
        },
      });

      const p1 = session.invoke("a.b", {});
      void p1.catch(() => {});
      expect(session.receiveBinaryWireFrame(replyFrame(1n, 1n))).not.toBeNull();

      // transId 2 lost in transit; 3 arrives with an accept verdict.
      const p3 = session.invoke("a.b", {});
      void p3.catch(() => {});
      expect(session.receiveBinaryWireFrame(replyFrame(3n, 2n))).not.toBeNull();
      expect(gaps).toEqual([["skip", 3n, 2n]]);

      // Sequence resynced: 4 is in-order again and raises no callback.
      const p4 = session.invoke("a.b", {});
      void p4.catch(() => {});
      expect(session.receiveBinaryWireFrame(replyFrame(4n, 3n))).not.toBeNull();
      expect(gaps).toHaveLength(1);
    });

    it("duplicate with drop verdict skips the frame without breaking the sequence", () => {
      const gaps: Array<[string, bigint, bigint]> = [];
      const session = new WireSession({
        sendFrame: () => true,
        invokeTimeoutMs: 60000,
        onTransIdGap: (kind, got, expected) => {
          gaps.push([kind, got, expected]);
          return "drop";
        },
      });

      const p1 = session.invoke("a.b", {});
      void p1.catch(() => {});
      expect(session.receiveBinaryWireFrame(replyFrame(1n, 1n))).not.toBeNull();

      // Redelivery of transId 1 is dropped, not processed twice.
      expect(session.receiveBinaryWireFrame(replyFrame(1n, 1n))).toBeNull();
      expect(gaps).toEqual([["duplicate", 1n, 2n]]);

      // 2 is still the expected successor.
      const p2 = session.invoke("a.b", {});
      void p2.catch(() => {});
      expect(session.receiveBinaryWireFrame(replyFrame(2n, 2n))).not.toBeNull();
      expect(gaps).toHaveLength(1);
    });

    it("default (no callback) drops mismatching frames like the legacy behavior", () => {
      const session = new WireSession({
        sendFrame: () => true,
        invokeTimeoutMs: 60000,
      });

      const p1 = session.invoke("a.b", {});
      void p1.catch(() => {});
      expect(session.receiveBinaryWireFrame(replyFrame(1n, 1n))).not.toBeNull();
      expect(session.receiveBinaryWireFrame(replyFrame(3n, 2n))).toBeNull();
      // lastTransId did not advance: 4 is still out of sequence.
      expect(session.receiveBinaryWireFrame(replyFrame(4n, 3n))).toBeNull();
    });
  });

  it("resumes subscriptions after reconnect", () => {
    const sent: unknown[] = [];
    const session = new WireSession({
      sendFrame: (frame) => { sent.push(frame); return true; },
      invokeTimeoutMs: 5000,
    });

    // Create a subscription
    const iterable = session.subscribe("test.resume", { tag: "a" });
    const iter = iterable[Symbol.asyncIterator]();

    const subFrame = sent.find((f: any) => f.type === "subscribe") as any;

    // Simulate that we received a chunk so sinceSeqNo advances
    session.dispatchAppFrame({ type: "chunk", subId: subFrame.subId, seqNo: 5, payload: "x" });

    // Capture pending and clear for resume simulation
    sent.length = 0;

    // Resume subscriptions (as transport would after reconnect)
    session.resumeSubscriptions();

    const resubFrame = sent.find((f: any) => f.type === "subscribe") as any;
    expect(resubFrame).toBeDefined();
    expect(resubFrame.callID).toBe("test.resume");
    expect(resubFrame.payload).toEqual({ tag: "a" });
    // Should resume from last seen seqNo
    expect(resubFrame.sinceSeqNo).toBe(5);

    iter.return?.();
  });
});
