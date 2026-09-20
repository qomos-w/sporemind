import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { WebSocketTransport } from "./ws";
import { PayloadEncoding, makeFlags, marshalWireFrame } from "./binary_frame";
import type { BinaryCodecLike, SchemaEntry, SchemaRegistryLike, TypeDesc } from "./schema";

/* ------------------------------------------------------------------ */
/* Mock WebSocket + CloseEvent for Node/vitest environment            */
/* ------------------------------------------------------------------ */

interface MockWS {
  url: string;
  binaryType: string;
  readyState: number;
  onopen: ((ev: Event) => void) | null;
  onclose: ((ev: any) => void) | null;
  onmessage: ((ev: MessageEvent) => void) | null;
  onerror: ((ev: Event) => void) | null;
  sent: unknown[];
  connect(): void;
  receive(data: unknown): void;
  close(code?: number, reason?: string): void;
  send(data: unknown): void;
}

let mockInstances: MockWS[] = [];
let activeTransports: WebSocketTransport[] = [];

function installMockWebSocket() {
  const MockWSClass = vi.fn((url: string) => {
    const instance: MockWS = {
      url,
      binaryType: "blob",
      readyState: 0, // CONNECTING
      onopen: null,
      onclose: null,
      onmessage: null,
      onerror: null,
      sent: [],
      connect() {
        this.readyState = 1; // OPEN
        this.onopen?.(new Event("open"));
      },
      receive(data: unknown) {
        this.onmessage?.(new MessageEvent("message", { data }));
      },
      close(code = 1000, reason = "") {
        this.readyState = 3; // CLOSED
        this.onclose?.({ type: "close", code, reason, wasClean: true } as any);
      },
      send(data: unknown) {
        this.sent.push(data);
      },
    };
    mockInstances.push(instance);
    return instance as unknown as WebSocket;
  }) as any;

  MockWSClass.CONNECTING = 0;
  MockWSClass.OPEN = 1;
  MockWSClass.CLOSING = 2;
  MockWSClass.CLOSED = 3;

  vi.stubGlobal("WebSocket", MockWSClass);
}

function uninstallMockWebSocket() {
  for (const t of activeTransports) {
    try { t.close(); } catch {}
  }
  activeTransports = [];
  vi.unstubAllGlobals();
  mockInstances = [];
}

function latestMock(): MockWS {
  if (mockInstances.length === 0) throw new Error("no mock WebSocket created");
  return mockInstances[mockInstances.length - 1];
}

function parseFrames(sent: unknown[]): unknown[] {
  return sent.map((data) => {
    if (typeof data === "string") return JSON.parse(data);
    if (data instanceof ArrayBuffer) return JSON.parse(new TextDecoder().decode(data));
    return data;
  });
}

function createTransport(options: ConstructorParameters<typeof WebSocketTransport>[0]): WebSocketTransport {
  const t = new WebSocketTransport(options);
  activeTransports.push(t);
  return t;
}

/* ------------------------------------------------------------------ */

describe("WebSocketTransport.subscribe", () => {
  beforeEach(() => {
    mockInstances = [];
    installMockWebSocket();
  });

  afterEach(() => {
    uninstallMockWebSocket();
  });

  it("sends subscribe frame after socket opens", async () => {
    const transport = createTransport({
      url: "ws://localhost/ws",
      lazyConnect: true,
      useBinary: false,
    });
    transport.connect();

    transport.subscribe("app.facts", { since: 0 });

    const ws = latestMock();
    ws.connect();

    const frames = parseFrames(ws.sent);
    const subFrame = frames.find((f: any) => f.type === "subscribe");
    expect(subFrame).toBeDefined();
    expect((subFrame as any).callID).toBe("app.facts");
    expect((subFrame as any).payload).toEqual({ since: 0 });

    transport.close();
  });

  it("yields chunks received from server", async () => {
    const transport = createTransport({
      url: "ws://localhost/ws",
      lazyConnect: true,
      useBinary: false,
    });
    transport.connect();

    const sub = transport.subscribe("app.facts", {});
    const iter = sub[Symbol.asyncIterator]();

    const ws = latestMock();
    ws.connect();

    const frames = parseFrames(ws.sent);
    const subFrame = frames.find((f: any) => f.type === "subscribe") as any;
    expect(subFrame?.subId).toBeTruthy();

    ws.receive(
      JSON.stringify({ type: "chunk", subId: subFrame.subId, payload: { id: 1 } })
    );
    const v1 = await iter.next();
    expect(v1.value).toEqual({ id: 1 });
    expect(v1.done).toBe(false);

    ws.receive(
      JSON.stringify({ type: "chunk", subId: subFrame.subId, payload: { id: 2 } })
    );
    const v2 = await iter.next();
    expect(v2.value).toEqual({ id: 2 });
    expect(v2.done).toBe(false);

    await iter.return?.();
    transport.close();
  });

  it("ends iteration on end frame", async () => {
    const transport = createTransport({
      url: "ws://localhost/ws",
      lazyConnect: true,
      useBinary: false,
    });
    transport.connect();

    const sub = transport.subscribe("app.facts", {});
    const iter = sub[Symbol.asyncIterator]();

    const ws = latestMock();
    ws.connect();

    const frames = parseFrames(ws.sent);
    const subFrame = frames.find((f: any) => f.type === "subscribe") as any;

    ws.receive(
      JSON.stringify({ type: "chunk", subId: subFrame.subId, payload: "hello" })
    );
    const v1 = await iter.next();
    expect(v1.value).toBe("hello");

    ws.receive(JSON.stringify({ type: "end", subId: subFrame.subId }));
    const v2 = await iter.next();
    expect(v2.done).toBe(true);

    transport.close();
  });

  it("throws on error frame", async () => {
    const transport = createTransport({
      url: "ws://localhost/ws",
      lazyConnect: true,
      useBinary: false,
    });
    transport.connect();

    const sub = transport.subscribe("app.facts", {});
    const iter = sub[Symbol.asyncIterator]();

    const ws = latestMock();
    ws.connect();

    const frames = parseFrames(ws.sent);
    const subFrame = frames.find((f: any) => f.type === "subscribe") as any;

    ws.receive(
      JSON.stringify({
        type: "error",
        subId: subFrame.subId,
        message: "stream failed",
      })
    );
    await expect(iter.next()).rejects.toThrow("stream failed");

    transport.close();
  });

  it("closes socket when server TransID has a gap", async () => {
    const transport = createTransport({
      url: "ws://localhost/ws",
      lazyConnect: true,
      useBinary: false,
    });
    transport.connect();

    const sub = transport.subscribe("app.facts", {});
    const iter = sub[Symbol.asyncIterator]();

    const ws = latestMock();
    ws.connect();

    const frames = parseFrames(ws.sent);
    const subFrame = frames.find((f: any) => f.type === "subscribe") as any;

    ws.receive(
      JSON.stringify({ type: "chunk", subId: subFrame.subId, transId: 1, payload: "a" })
    );
    await iter.next();

    // Skip transId 2 — this should trigger a close.
    ws.receive(
      JSON.stringify({ type: "chunk", subId: subFrame.subId, transId: 3, payload: "b" })
    );

    // Give the transport a tick to process the message and close.
    await new Promise((resolve) => setTimeout(resolve, 10));

    expect(ws.readyState).toBe(3); // CLOSED

    transport.close();
  });

  it("sends unsubscribe frame when last iterator is disposed", async () => {
    const transport = createTransport({
      url: "ws://localhost/ws",
      lazyConnect: true,
      useBinary: false,
    });
    transport.connect();

    const sub = transport.subscribe("app.facts", {});
    const iter = sub[Symbol.asyncIterator]();

    const ws = latestMock();
    ws.connect();

    const frames = parseFrames(ws.sent);
    const subFrame = frames.find((f: any) => f.type === "subscribe") as any;

    await iter.return?.();

    const allFrames = parseFrames(ws.sent);
    const unsubFrame = allFrames.find((f: any) => f.type === "unsubscribe");
    expect(unsubFrame).toBeDefined();
    expect((unsubFrame as any).subId).toBe(subFrame.subId);

    transport.close();
  });

  it("resumes subscriptions after reconnect", async () => {
    const transport = createTransport({
      url: "ws://localhost/ws",
      lazyConnect: true,
      useBinary: false,
    });
    transport.connect();

    const sub = transport.subscribe("app.facts", { tag: "a" });
    const iter = sub[Symbol.asyncIterator]();

    const ws1 = latestMock();
    ws1.connect();

    const frames1 = parseFrames(ws1.sent);
    const subFrame1 = frames1.find((f: any) => f.type === "subscribe") as any;

    // Server sends a chunk so sinceSeqNo advances
    ws1.receive(
      JSON.stringify({
        type: "chunk",
        subId: subFrame1.subId,
        seqNo: 5,
        payload: "x",
      })
    );
    await iter.next();

    // Server closes connection
    ws1.close(1006, "abnormal");

    // Wait for reconnect schedule (1s base delay)
    await new Promise((r) => setTimeout(r, 1100));

    const ws2 = latestMock();
    ws2.connect();

    const frames2 = parseFrames(ws2.sent);
    const subFrame2 = frames2.find((f: any) => f.type === "subscribe") as any;
    expect(subFrame2).toBeDefined();
    expect(subFrame2.callID).toBe("app.facts");
    expect(subFrame2.payload).toEqual({ tag: "a" });
    // Should resume from last seen seqNo
    expect(subFrame2.sinceSeqNo).toBe(5);

    await iter.return?.();
    transport.close();
  }, 3000);

  it("does not send unsubscribe after socket closes", async () => {
    const transport = createTransport({
      url: "ws://localhost/ws",
      lazyConnect: true,
      useBinary: false,
    });
    transport.connect();

    const sub = transport.subscribe("app.facts", {});
    const iter = sub[Symbol.asyncIterator]();

    const ws = latestMock();
    ws.connect();
    ws.close(1000, "done");

    await new Promise((r) => setTimeout(r, 50));

    // Socket is closed, disposing iterator should not try to send unsubscribe
    await iter.return?.();

    const allFrames = parseFrames(ws.sent);
    const unsubFrames = allFrames.filter((f: any) => f.type === "unsubscribe");
    expect(unsubFrames.length).toBe(0);

    transport.close();
  });
});

/* ------------------------------------------------------------------ */
/* Binary-only mode tests                                             */
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

const mockBinaryCodec: BinaryCodecLike = {
  encode: (_desc, value) => mockEncode(value),
  decode: (_desc, data) => mockDecode(data),
};

function makeMockRegistry(entries: SchemaEntry[]): SchemaRegistryLike {
  const byNsId = new Map<string, SchemaEntry>();
  const byId = new Map<number, SchemaEntry>();
  for (const e of entries) {
    byNsId.set(`${e.namespace}:${e.schemaId}`, e);
    byId.set(e.schemaId, e);
  }
  return {
    get: (namespace: string, schemaId: number) => byNsId.get(`${namespace}:${schemaId}`),
    getById: (schemaId: number) => byId.get(schemaId),
    objectFor: (_namespace: string) => (_name: string) => undefined,
  };
}

function makeStructType(name: string, schemaId: number): TypeDesc {
  return { kind: "struct", name, className: name, classId: schemaId };
}

function binaryReplyFrame(reqId: number, payload: unknown): ArrayBuffer {
  const payloadBytes = mockEncode(payload);
  const wire = marshalWireFrame({
    flags: makeFlags(PayloadEncoding.Binary, 0),
    type: 2, // Reply
    corId: BigInt(reqId),
    seq: 0,
    transId: 0n,
    callId: "",
    subId: "",
    target: "",
    from: "",
    errorMsg: "",
    payload: payloadBytes,
    headers: new Map(),
  });
  return wire.buffer.slice(wire.byteOffset, wire.byteOffset + wire.byteLength) as ArrayBuffer;
}

describe("WebSocketTransport binary-only", () => {
  beforeEach(() => {
    mockInstances = [];
    installMockWebSocket();
  });

  afterEach(() => {
    uninstallMockWebSocket();
  });

  it("throws when invoking with payload but no schema info", async () => {
    const transport = createTransport({
      url: "ws://localhost/ws",
      lazyConnect: true,
      binaryOnly: true,
    });
    transport.connect();
    const ws = latestMock();
    ws.connect();

    await expect(transport.invoke("app.test", { value: 1 })).rejects.toThrow(
      "binary-only: cannot encode struct payload"
    );
    transport.close();
  });

  it("throws when schema entry is missing", async () => {
    const registry = makeMockRegistry([]);
    const transport = createTransport({
      url: "ws://localhost/ws",
      lazyConnect: true,
      binaryOnly: true,
      schemaRegistry: registry,
      binaryCodec: mockBinaryCodec,
    });
    transport.connect();
    const ws = latestMock();
    ws.connect();

    await expect(
      transport.invoke("app.test", { value: 1 }, { reqSchemaId: 1, resSchemaId: 2 })
    ).rejects.toThrow("binary-only: missing schema entry");
    transport.close();
  });

  it("sends TBC-encoded invoke payload when schema is known", async () => {
    const registry = makeMockRegistry([
      {
        namespace: "app",
        schemaId: 10,
        name: "TestReq",
        type: makeStructType("TestReq", 10),
      },
    ]);
    const transport = createTransport({
      url: "ws://localhost/ws",
      lazyConnect: true,
      binaryOnly: true,
      schemaRegistry: registry,
      binaryCodec: mockBinaryCodec,
    });
    transport.connect();
    const ws = latestMock();
    ws.connect();

    const pending = transport.invoke("app.test", { value: 1 }, { reqSchemaId: 10 });

    // sendBinary is async; give it a microtask to encode and send.
    await new Promise((r) => setTimeout(r, 0));

    expect(ws.sent.length).toBe(1);
    expect(ws.sent[0]).toBeInstanceOf(ArrayBuffer);
    transport.close();
    await expect(pending).rejects.toThrow("transport closed");
  });

  it("rejects text frames by closing the connection", async () => {
    const transport = createTransport({
      url: "ws://localhost/ws",
      lazyConnect: true,
      binaryOnly: true,
    });
    transport.connect();
    const ws = latestMock();
    ws.connect();

    const closeSpy = vi.spyOn(ws, "close");
    ws.receive(JSON.stringify({ type: "reply", reqId: 1, payload: {} }));

    expect(closeSpy).toHaveBeenCalledWith(4001, "text frame not allowed");
    transport.close();
  });

  it("rejects JSON-encoded reply payloads", async () => {
    const registry = makeMockRegistry([
      {
        namespace: "app",
        schemaId: 20,
        name: "TestResp",
        type: makeStructType("TestResp", 20),
      },
    ]);
    const transport = createTransport({
      url: "ws://localhost/ws",
      lazyConnect: true,
      binaryOnly: true,
      schemaRegistry: registry,
      binaryCodec: mockBinaryCodec,
    });
    transport.connect();
    const ws = latestMock();
    ws.connect();

    const pending = transport.invoke("app.test", undefined, { resSchemaId: 20 });

    // Build a JSON-encoded reply wire frame.
    const payloadBytes = new TextEncoder().encode(JSON.stringify({ value: 1 }));
    const wire = marshalWireFrame({
      flags: makeFlags(PayloadEncoding.JSON, 0),
      type: 2,
      corId: 1n,
      seq: 0,
      transId: 0n,
      callId: "",
      subId: "",
      target: "",
      from: "",
      errorMsg: "",
      payload: payloadBytes,
      headers: new Map(),
    });
    const buf = wire.buffer.slice(wire.byteOffset, wire.byteOffset + wire.byteLength);
    ws.receive(buf);

    await expect(pending).rejects.toThrow("binary-only: received JSON-encoded payload");
    transport.close();
  });

  it("encodes event subscription request with system builtin schema", async () => {
    const transport = createTransport({
      url: "ws://localhost/ws",
      lazyConnect: true,
      binaryOnly: true,
      binaryCodec: mockBinaryCodec,
    });
    transport.connect();

    const sub = transport.subscribe(
      "gospore.events.subscribe_service",
      { serviceName: "workspace", kind: "mounts" },
      { reqSchemaId: 66 }
    );
    const iter = sub[Symbol.asyncIterator]();

    const ws = latestMock();
    ws.connect();

    await new Promise((r) => setTimeout(r, 0));

    expect(ws.sent.length).toBe(1);
    expect(ws.sent[0]).toBeInstanceOf(ArrayBuffer);

    await iter.return?.();
    transport.close();
  });

  it("decodes binary reply payload with known schema", async () => {
    const registry = makeMockRegistry([
      {
        namespace: "app",
        schemaId: 20,
        name: "TestResp",
        type: makeStructType("TestResp", 20),
      },
    ]);
    const transport = createTransport({
      url: "ws://localhost/ws",
      lazyConnect: true,
      binaryOnly: true,
      schemaRegistry: registry,
      binaryCodec: mockBinaryCodec,
    });
    transport.connect();
    const ws = latestMock();
    ws.connect();

    const pending = transport.invoke("app.test", undefined, { resSchemaId: 20 });
    ws.receive(binaryReplyFrame(1, { value: 42 }));

    await expect(pending).resolves.toEqual({ value: 42 });
    transport.close();
  });
});

/* ------------------------------------------------------------------ */
/* Resilience: jitter, online/offline, visibility                      */
/* ------------------------------------------------------------------ */

describe("WebSocketTransport resilience", () => {
  beforeEach(() => {
    mockInstances = [];
    installMockWebSocket();
  });

  afterEach(() => {
    uninstallMockWebSocket();
  });

  it("applies jitter to reconnect delay", () => {
    vi.useFakeTimers();
    // Math.random() = 0.5 → jitter = floor(0.5 * 500) = 250ms
    const randomSpy = vi.spyOn(Math, "random").mockReturnValue(0.5);

    const transport = createTransport({
      url: "ws://localhost/ws",
      lazyConnect: true,
      useBinary: false,
      reconnectBaseMs: 100,
      reconnectMaxMs: 30000,
      reconnectJitterMs: 500,
    });
    transport.connect();
    const ws = latestMock();
    ws.connect();

    // Close → scheduleReconnect fires with delay = 100 + 250 = 350ms
    ws.close(1006, "abnormal");

    vi.advanceTimersByTime(300);
    expect(mockInstances.length).toBe(1); // not yet

    vi.advanceTimersByTime(50);
    expect(mockInstances.length).toBe(2); // reconnected at 350ms

    randomSpy.mockRestore();
    vi.useRealTimers();
    transport.close();
  });

  it("keeps growing the backoff when connections die quickly", () => {
    vi.useFakeTimers();

    const transport = createTransport({
      url: "ws://localhost/ws",
      lazyConnect: true,
      useBinary: false,
      reconnectBaseMs: 100,
      reconnectMaxMs: 30000,
      heartbeatIntervalMs: 60000,
      heartbeatTimeoutMs: 60000,
    });
    transport.connect();
    let ws = latestMock();
    ws.connect();

    // First quick death → delay = base (100ms)
    ws.close(1006, "abnormal");
    vi.advanceTimersByTime(100);
    expect(mockInstances.length).toBe(2);

    // Second quick death → delay must grow to base*2 (200ms), not reset to base
    ws = latestMock();
    ws.connect();
    ws.close(1006, "abnormal");

    vi.advanceTimersByTime(100);
    expect(mockInstances.length).toBe(2); // still waiting — delay is 200ms now

    vi.advanceTimersByTime(100);
    expect(mockInstances.length).toBe(3); // reconnected at 200ms

    vi.useRealTimers();
    transport.close();
  });

  it("resets the backoff after a stable connection", () => {
    vi.useFakeTimers();

    const transport = createTransport({
      url: "ws://localhost/ws",
      lazyConnect: true,
      useBinary: false,
      reconnectBaseMs: 100,
      reconnectMaxMs: 30000,
      reconnectStableMs: 10000,
      heartbeatIntervalMs: 60000,
      heartbeatTimeoutMs: 60000,
    });
    transport.connect();
    let ws = latestMock();
    ws.connect();

    // Quick death → delay 100ms
    ws.close(1006, "abnormal");
    vi.advanceTimersByTime(100);
    expect(mockInstances.length).toBe(2);

    // Second connection lives 15s (> reconnectStableMs) → attempts reset
    ws = latestMock();
    ws.connect();
    vi.advanceTimersByTime(15000);
    ws.close(1006, "abnormal");

    // Delay reset back to base (100ms)
    vi.advanceTimersByTime(100);
    expect(mockInstances.length).toBe(3);

    vi.useRealTimers();
    transport.close();
  });

  it("reconnects immediately on browser online event", () => {
    const winListeners: Record<string, EventListener> = {};
    vi.stubGlobal("window", {
      addEventListener: vi.fn((ev: string, h: EventListener) => { winListeners[ev] = h; }),
      removeEventListener: vi.fn(),
    });
    vi.stubGlobal("document", {
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      visibilityState: "visible",
    });

    vi.useFakeTimers();

    const transport = createTransport({
      url: "ws://localhost/ws",
      lazyConnect: true,
      useBinary: false,
      reconnectBaseMs: 5000,
      reconnectMaxMs: 30000,
    });
    transport.connect();
    const ws = latestMock();
    ws.connect();

    ws.close(1006, "abnormal");

    // Reconnect is pending at 5000ms — not yet fired
    expect(mockInstances.length).toBe(1);

    // Fire online event → should cancel pending timer and reconnect now
    winListeners["online"]?.(new Event("online"));
    expect(mockInstances.length).toBe(2);

    vi.useRealTimers();
    vi.unstubAllGlobals();
    transport.close();
  });

  it("sends immediate heartbeat ping on visibility change to visible", () => {
    const docListeners: Record<string, EventListener> = {};
    vi.stubGlobal("window", {
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    });
    vi.stubGlobal("document", {
      addEventListener: vi.fn((ev: string, h: EventListener) => { docListeners[ev] = h; }),
      removeEventListener: vi.fn(),
      visibilityState: "visible",
    });

    const transport = createTransport({
      url: "ws://localhost/ws",
      lazyConnect: true,
      useBinary: false,
      heartbeatIntervalMs: 60000,
    });
    transport.connect();
    const ws = latestMock();
    ws.connect();

    ws.sent.length = 0;

    docListeners["visibilitychange"]?.(new Event("visibilitychange"));

    const frames = parseFrames(ws.sent);
    const ping = frames.find((f: any) => f.type === "ping");
    expect(ping).toBeDefined();

    vi.unstubAllGlobals();
    transport.close();
  });

  it("does not ping when visibility changes to hidden", () => {
    const docListeners: Record<string, EventListener> = {};
    vi.stubGlobal("window", {
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    });
    vi.stubGlobal("document", {
      addEventListener: vi.fn((ev: string, h: EventListener) => { docListeners[ev] = h; }),
      removeEventListener: vi.fn(),
      visibilityState: "hidden",
    });

    const transport = createTransport({
      url: "ws://localhost/ws",
      lazyConnect: true,
      useBinary: false,
      heartbeatIntervalMs: 60000,
    });
    transport.connect();
    const ws = latestMock();
    ws.connect();

    ws.sent.length = 0;

    docListeners["visibilitychange"]?.(new Event("visibilitychange"));

    const frames = parseFrames(ws.sent);
    const ping = frames.find((f: any) => f.type === "ping");
    expect(ping).toBeUndefined();

    vi.unstubAllGlobals();
    transport.close();
  });
});
