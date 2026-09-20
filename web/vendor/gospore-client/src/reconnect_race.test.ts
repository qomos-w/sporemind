import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { WebSocketTransport } from "./ws";
import type { WebSocketLike } from "./ws";

/* ------------------------------------------------------------------ */
/* Deferred-close mock WebSocket                                       */
/*                                                                     */
/* Real browsers always dispatch the close event asynchronously after  */
/* close() returns. The existing ws.test.ts mock fires onclose         */
/* synchronously, which cannot reproduce the mobile app-resume race:   */
/* forceReconnectClient() → transport.reconnect() closes the healthy   */
/* socket and immediately opens a new one, then the OLD socket's late  */
/* close event lands on the state machine.                             */
/* ------------------------------------------------------------------ */

class DeferredMockWS {
  static instances: DeferredMockWS[] = [];

  url: string;
  binaryType = "blob";
  readyState = 0; // CONNECTING
  onopen: ((ev: Event) => void) | null = null;
  onclose: ((ev: { code: number; reason: string; wasClean: boolean }) => void) | null = null;
  onmessage: ((ev: MessageEvent) => void) | null = null;
  onerror: ((ev: Event) => void) | null = null;
  sent: unknown[] = [];
  closeCalls = 0;

  constructor(url: string) {
    this.url = url;
    DeferredMockWS.instances.push(this);
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

  close(code = 1000, reason = ""): void {
    this.closeCalls++;
    if (this.readyState >= 2) return;
    this.readyState = 2; // CLOSING
    queueMicrotask(() => {
      this.readyState = 3; // CLOSED
      this.onclose?.({ code, reason, wasClean: true });
    });
  }
}

const flush = () => new Promise<void>((r) => queueMicrotask(() => queueMicrotask(r)));
const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

let activeTransports: WebSocketTransport[] = [];

function makeTransport(onReconnecting?: () => void): WebSocketTransport {
  const t = new WebSocketTransport({
    url: "ws://test/ws",
    lazyConnect: true,
    useBinary: false,
    createSocket: (url) => new DeferredMockWS(url) as unknown as WebSocketLike,
    reconnectBaseMs: 30,
    reconnectMaxMs: 30,
    reconnectJitterMs: 0,
    // Keep heartbeat out of the test window.
    heartbeatIntervalMs: 600_000,
    heartbeatTimeoutMs: 600_000,
    onReconnecting,
  });
  activeTransports.push(t);
  return t;
}

describe("WebSocketTransport forced-reconnect race (mobile app resume)", () => {
  beforeEach(() => {
    DeferredMockWS.instances = [];
  });

  afterEach(() => {
    for (const t of activeTransports) {
      try { t.close(); } catch { /* ignore */ }
    }
    activeTransports = [];
  });

  it("stale close from the pre-reconnect socket does not schedule another reconnect", async () => {
    const onReconnecting = vi.fn();
    const t = makeTransport(onReconnecting);
    t.connect();
    const ws1 = DeferredMockWS.instances[0];
    ws1.open();

    // App returns to foreground with a healthy connection: force reconnect.
    t.reconnect();
    expect(DeferredMockWS.instances).toHaveLength(2);
    const ws2 = DeferredMockWS.instances[1];
    ws2.open();

    // The old socket's close handshake completes AFTER the new one is open.
    await flush();
    // Long enough for any wrongly-scheduled reconnect timer to fire.
    await sleep(80);

    expect(onReconnecting).not.toHaveBeenCalled();
    expect(DeferredMockWS.instances).toHaveLength(2);

    // The new socket is the live one.
    const p = t.invoke("sys.ping", {});
    void p.catch(() => {});
    expect(ws2.sent.length).toBeGreaterThan(0);
    expect(ws1.sent).toHaveLength(0);
  });

  it("back-to-back forced reconnects leave a single live socket", async () => {
    const onReconnecting = vi.fn();
    const t = makeTransport(onReconnecting);
    t.connect();
    const ws1 = DeferredMockWS.instances[0];
    ws1.open();

    // app-resumed postMessage + visibilitychange + online can arrive within
    // the same tick on Capacitor; each forces a reconnect.
    t.reconnect();
    t.reconnect();
    t.reconnect();

    const instances = DeferredMockWS.instances;
    expect(instances).toHaveLength(4);
    const live = instances[instances.length - 1];
    live.open();

    // Late events from every superseded socket must be ignored.
    await flush();
    for (const stale of instances.slice(0, -1)) {
      expect(stale.onopen).toBeNull();
      expect(stale.onmessage).toBeNull();
      expect(stale.onclose).toBeNull();
      expect(stale.onerror).toBeNull();
      stale.open(); // stale open must not hijack the state machine
      stale.receive(JSON.stringify({ type: "pong", transId: 99 }));
    }
    await flush();
    await sleep(80);

    expect(onReconnecting).not.toHaveBeenCalled();
    expect(DeferredMockWS.instances).toHaveLength(4);

    const p = t.invoke("sys.ping", {});
    void p.catch(() => {});
    expect(live.sent.length).toBeGreaterThan(0);
    expect(ws1.sent).toHaveLength(0);
  });
});
