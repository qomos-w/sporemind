import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { WebSocketTransport } from "./ws";
import type { WebSocketLike } from "./ws";

/* A socket that never completes the upgrade handshake: onopen/onerror/onclose
 * never fire, and close() is a no-op. This is the exact failure mode a proxy or
 * a wedged server that accepts TCP but never answers the WS upgrade produces,
 * and which previously left the transport stuck in "connecting" forever. */
class HungMockWS {
  static instances: HungMockWS[] = [];
  url: string;
  binaryType = "blob" as BinaryType;
  readyState = 0;
  onopen: ((ev: Event) => void) | null = null;
  onclose: ((ev: { code: number; reason: string; wasClean: boolean }) => void) | null = null;
  onmessage: ((ev: MessageEvent) => void) | null = null;
  onerror: ((ev: Event) => void) | null = null;
  sent: unknown[] = [];
  closeCalls = 0;

  constructor(url: string) {
    this.url = url;
    HungMockWS.instances.push(this);
  }
  open(): void {
    this.readyState = 1;
    this.onopen?.(new Event("open"));
  }
  receive(data: unknown): void {
    this.onmessage?.(new MessageEvent("message", { data }));
  }
  send(data: unknown): void {
    this.sent.push(data);
  }
  close(): void {
    // Deliberately a no-op: a hung handshake never emits a close event.
    this.closeCalls++;
  }
}

const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

let activeTransports: WebSocketTransport[] = [];

function makeTransport(connectTimeoutMs: number, onReconnecting?: () => void): WebSocketTransport {
  const t = new WebSocketTransport({
    url: "ws://test/ws",
    lazyConnect: true,
    useBinary: false,
    createSocket: (url) => new HungMockWS(url) as unknown as WebSocketLike,
    reconnectBaseMs: 30,
    reconnectMaxMs: 30,
    reconnectJitterMs: 0,
    heartbeatIntervalMs: 600_000,
    heartbeatTimeoutMs: 600_000,
    connectTimeoutMs,
    onReconnecting,
  });
  activeTransports.push(t);
  return t;
}

describe("WebSocketTransport connect-establishment timeout", () => {
  beforeEach(() => {
    HungMockWS.instances = [];
  });
  afterEach(() => {
    for (const t of activeTransports) {
      try { t.close(); } catch { /* ignore */ }
    }
    activeTransports = [];
  });

  it("recovers from a hung handshake by reconnecting", async () => {
    const onReconnecting = vi.fn();
    const t = makeTransport(20, onReconnecting);
    t.connect();

    // First socket is created and immediately stuck in CONNECTING.
    expect(HungMockWS.instances).toHaveLength(1);
    const ws1 = HungMockWS.instances[0];
    expect(ws1.readyState).toBe(0);

    // Wait past the connect timeout + the reconnect backoff.
    await sleep(120);

    // The hung socket was best-effort closed and a second attempt made.
    expect(ws1.closeCalls).toBeGreaterThanOrEqual(1);
    expect(onReconnecting).toHaveBeenCalled();
    expect(HungMockWS.instances.length).toBeGreaterThanOrEqual(2);

    // The newest socket becomes live once it opens.
    const ws2 = HungMockWS.instances[HungMockWS.instances.length - 1];
    ws2.open();
    const p = t.invoke("sys.ping", {});
    void p.catch(() => {});
    await sleep(5);
    expect(ws2.sent.length).toBeGreaterThan(0);
    expect(ws1.sent).toHaveLength(0);
  });

  it("does not reconnect when the handshake completes in time", async () => {
    const onReconnecting = vi.fn();
    const t = makeTransport(50, onReconnecting);
    t.connect();
    const ws1 = HungMockWS.instances[0];
    ws1.open(); // handshake completes well within the timeout

    await sleep(120);
    expect(onReconnecting).not.toHaveBeenCalled();
    expect(HungMockWS.instances).toHaveLength(1);
  });
});
