import { describe, it, expect, vi, type Mock } from "vitest";
import { GosporeClient } from "./client";
import type { Transport, ManagedTransport } from "./transport";

/* ------------------------------------------------------------------ */
/* Stub transports for testing the lifecycle facade                   */
/* ------------------------------------------------------------------ */

function stubTransport(): Transport {
  return {
    invoke: vi.fn().mockResolvedValue(null),
    subscribe: vi.fn().mockReturnValue({
      [Symbol.asyncIterator]: () => ({
        next: vi.fn().mockResolvedValue({ value: undefined, done: true }),
      }),
    }),
    close: vi.fn(),
  };
}

function stubManagedTransport(): ManagedTransport & Transport {
  const handlers = new Set<(info: { isReconnect: boolean }) => void>();
  let readyResolve: (() => void) | null = null;

  return {
    invoke: vi.fn().mockResolvedValue(null),
    subscribe: vi.fn().mockReturnValue({
      [Symbol.asyncIterator]: () => ({
        next: vi.fn().mockResolvedValue({ value: undefined, done: true }),
      }),
    }),
    close: vi.fn(),

    connect: vi.fn(() => {
      // Simulate connection by resolving ready promise
      readyResolve?.();
    }),
    waitUntilReady: vi.fn(() => new Promise<void>((resolve) => { readyResolve = resolve; })),
    reconnect: vi.fn(function (this: any) {
      // For test we just call connect
      this.connect();
    }),
    onConnected: vi.fn((handler: (info: { isReconnect: boolean }) => void) => {
      handlers.add(handler);
      return () => { handlers.delete(handler); };
    }),
  };
}

/* ------------------------------------------------------------------ */
/* subscribeEvents revival race                                        */
/* ------------------------------------------------------------------ */

interface MockIter {
  iterable: { [Symbol.asyncIterator]: () => AsyncIterator<unknown> };
  returnSpy: Mock<[], Promise<IteratorResult<unknown>>>;
}

function makeParkingIterable(): MockIter {
  let pending: ((r: IteratorResult<unknown>) => void) | null = null;
  const returnSpy = vi.fn(async (): Promise<IteratorResult<unknown>> => {
    pending?.({ value: undefined, done: true });
    pending = null;
    return { value: undefined, done: true };
  });
  const iter = {
    next: () => new Promise<IteratorResult<unknown>>((resolve) => { pending = resolve; }),
    return: returnSpy,
  };
  return { iterable: { [Symbol.asyncIterator]: () => iter }, returnSpy };
}

describe("GosporeClient.events shared-bucket revival race", () => {
  const settle = () => new Promise<void>((r) => setTimeout(r, 10));

  it("cancel of a revived bucket still disposes its stream after the old pump exits", async () => {
    const streams: MockIter[] = [];
    const t: Transport = {
      invoke: vi.fn().mockResolvedValue(null),
      subscribe: vi.fn(() => {
        const s = makeParkingIterable();
        streams.push(s);
        return s.iterable;
      }),
      close: vi.fn(),
    };
    const client = new GosporeClient(t);

    const cancelA = client.events.onService("svc", "k", () => {});
    await settle();
    expect(t.subscribe).toHaveBeenCalledTimes(1);

    // Last handler of stream A leaves → cancel A; its pump exits async.
    cancelA();

    // Revival: a new handler mounts before A's pump finished exiting,
    // creating a replacement bucket under the same key.
    const cancelB = client.events.onService("svc", "k", () => {});
    await settle();
    expect(t.subscribe).toHaveBeenCalledTimes(2);

    // A's pump must NOT have deleted B's bucket from the shared map.
    const cancelC = client.events.onService("svc", "k", () => {});
    await settle();
    expect(t.subscribe).toHaveBeenCalledTimes(2); // B's bucket reused, no third stream
    cancelC();

    // Cancelling B's last handler must dispose stream B (send unsubscribe).
    cancelB();
    await settle();
    expect(streams[0].returnSpy).toHaveBeenCalled();
    expect(streams[1].returnSpy).toHaveBeenCalled();
  });
});

describe("GosporeClient lifecycle facade", () => {
  it("connect() is a no-op for plain Transport", () => {
    const t = stubTransport();
    const client = new GosporeClient(t);
    expect(() => client.connect()).not.toThrow();
    // No error means the method existed and was callable
  });

  it("connect() delegates to ManagedTransport.connect", () => {
    const mt = stubManagedTransport();
    const client = new GosporeClient(mt);
    client.connect();
    expect(mt.connect).toHaveBeenCalledOnce();
  });

  it("preserves lifecycle through auth wrapper", async () => {
    const mt = stubManagedTransport();
    const client = new GosporeClient(mt, { auth: { getToken: () => "token" } });
    const ready = client.waitUntilReady();
    client.connect();
    expect(mt.connect).toHaveBeenCalledOnce();
    await expect(ready).resolves.toBeUndefined();
    client.reconnect();
    expect(mt.reconnect).toHaveBeenCalledOnce();
  });

  it("waitUntilReady() resolves immediately for plain Transport", async () => {
    const t = stubTransport();
    const client = new GosporeClient(t);
    await expect(client.waitUntilReady()).resolves.toBeUndefined();
  });

  it("waitUntilReady() delegates to ManagedTransport.waitUntilReady", async () => {
    const mt = stubManagedTransport();
    const client = new GosporeClient(mt);
    const p = client.waitUntilReady();
    // The stub resolves on connect()
    client.connect();
    await expect(p).resolves.toBeUndefined();
    expect(mt.waitUntilReady).toHaveBeenCalled();
  });

  it("reconnect() is a no-op for plain Transport", () => {
    const t = stubTransport();
    const client = new GosporeClient(t);
    expect(() => client.reconnect()).not.toThrow();
  });

  it("reconnect() delegates to ManagedTransport.reconnect", () => {
    const mt = stubManagedTransport();
    const client = new GosporeClient(mt);
    client.reconnect();
    expect(mt.reconnect).toHaveBeenCalledOnce();
  });

  it("onConnected() returns no-op unsubscriber for plain Transport", () => {
    const t = stubTransport();
    const client = new GosporeClient(t);
    const unsub = client.onConnected(() => {});
    expect(typeof unsub).toBe("function");
    expect(() => unsub()).not.toThrow();
  });

  it("onConnected() delegates to ManagedTransport.onConnected", () => {
    const mt = stubManagedTransport();
    const client = new GosporeClient(mt);
    const handler = vi.fn();
    const unsub = client.onConnected(handler);
    expect(mt.onConnected).toHaveBeenCalledWith(handler);
    expect(typeof unsub).toBe("function");
  });

  it("onConnected() fires when ManagedTransport connects", () => {
    const mt = stubManagedTransport() as ManagedTransport & { connect: ReturnType<typeof vi.fn> };
    const client = new GosporeClient(mt);
    const handler = vi.fn();
    client.onConnected(handler);

    // Simulate a connect by calling the handler directly via the transport's onConnected
    // We stored a reference in the stub — let's get the handler that was registered
    const registeredHandler = (mt.onConnected as ReturnType<typeof vi.fn>).mock.calls[0][0] as (info: { isReconnect: boolean }) => void;
    registeredHandler({ isReconnect: false });

    expect(handler).toHaveBeenCalledWith({ isReconnect: false });
  });

  it("invoke/subscribe/close/getTransport still work unchanged", async () => {
    const t = stubTransport();
    const client = new GosporeClient(t);

    client.invoke("test.call", {});
    expect(t.invoke).toHaveBeenCalledWith("test.call", {}, undefined);

    // subscribe is a generator — must iterate to trigger transport.subscribe
    const iter = client.subscribe("test.stream", {})[Symbol.asyncIterator]();
    await iter.next();
    expect(t.subscribe).toHaveBeenCalledWith("test.stream", {}, undefined);

    client.close();
    expect(t.close).toHaveBeenCalledOnce();

    expect(client.getTransport()).toBe(t);
  });
});
