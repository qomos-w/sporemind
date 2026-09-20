import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { WebSocketTransport } from "./ws";
import type { WebSocketLike } from "./ws";
import type { TransIdGapInfo } from "./frame_transport";

/* Gap observability: the tolerate policy exists for lossy channels (Wails
 * event bridges). The skip itself is by design, but it must be countable —
 * hosts need the loss rate to decide when a stream deserves a reliable
 * channel instead. These tests pin gapStats() and the onTransIdGap hook on
 * the ManagedFrameTransport text-frame path. */

class GapMockWS {
  url: string;
  binaryType = "blob" as BinaryType;
  readyState = 0;
  onopen: ((ev: Event) => void) | null = null;
  onclose: ((ev: { code: number; reason: string; wasClean: boolean }) => void) | null = null;
  onmessage: ((ev: MessageEvent) => void) | null = null;
  onerror: ((ev: Event) => void) | null = null;
  sent: unknown[] = [];

  constructor(url: string) {
    this.url = url;
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
    this.readyState = 3;
    this.onclose?.({ code: 1000, reason: "", wasClean: true });
  }
}

let transport: WebSocketTransport | null = null;

function makeTransport(gaps: TransIdGapInfo[]): WebSocketTransport {
  const t = new WebSocketTransport({
    url: "ws://test/ws",
    lazyConnect: true,
    useBinary: false,
    gapPolicy: "tolerate",
    onTransIdGap: (info) => gaps.push(info),
    createSocket: (url) => new GapMockWS(url) as unknown as WebSocketLike,
    reconnectBaseMs: 30,
    reconnectMaxMs: 30,
    reconnectJitterMs: 0,
    heartbeatIntervalMs: 600_000,
    heartbeatTimeoutMs: 600_000,
  });
  transport = t;
  return t;
}

/** Push a server text frame with the given transId through the socket. */
function pushFrame(sock: GapMockWS, transId: number, type: string, payload: unknown): void {
  sock.receive(JSON.stringify({ transId, type, payload }));
}

describe("transId gap observability", () => {
  let gaps: TransIdGapInfo[];

  beforeEach(() => {
    gaps = [];
  });

  afterEach(() => {
    transport?.close();
    transport = null;
  });

  it("counts skips and lost frames, fires onTransIdGap per break", async () => {
    const t = makeTransport(gaps);
    await t.connect();
    const sock = (t["connection"] as unknown as { ws: GapMockWS }).ws;

    pushFrame(sock, 10, "ok", {});
    pushFrame(sock, 11, "ok", {});
    // skip: 12..45 lost (34 frames)
    pushFrame(sock, 46, "ok", {});
    // duplicate: 46 again
    pushFrame(sock, 46, "ok", {});
    // skip again: 47..49 lost
    pushFrame(sock, 50, "ok", {});

    expect(t.gapStats()).toEqual({ skips: 2, duplicates: 1, lostFrames: 37 });
    expect(gaps.map((g) => [g.kind, g.lost])).toEqual([
      ["skip", 34],
      ["duplicate", 0],
      ["skip", 3],
    ]);
  });

  it("keeps counters across reconnects (channel loss rate, not per-conn)", async () => {
    const t = makeTransport(gaps);
    await t.connect();
    let sock = (t["connection"] as unknown as { ws: GapMockWS }).ws;
    sock.open();
    pushFrame(sock, 1, "ok", {});
    pushFrame(sock, 5, "ok", {});
    expect(t.gapStats().lostFrames).toBe(3);

    // Simulate a transport-level drop (not client close()): the auto
    // reconnect creates a fresh server-side sequence from 1.
    sock.close();
    await new Promise((r) => setTimeout(r, 120));
    sock = (t["connection"] as unknown as { ws: GapMockWS }).ws;
    sock.open();
    pushFrame(sock, 1, "ok", {});
    pushFrame(sock, 3, "ok", {});
    expect(t.gapStats()).toEqual({ skips: 2, duplicates: 0, lostFrames: 4 });
  });

  it("no gaps reported on a clean sequence", async () => {
    const t = makeTransport(gaps);
    await t.connect();
    const sock = (t["connection"] as unknown as { ws: GapMockWS }).ws;
    for (let i = 1; i <= 5; i++) pushFrame(sock, i, "ok", {});
    expect(t.gapStats()).toEqual({ skips: 0, duplicates: 0, lostFrames: 0 });
    expect(gaps).toEqual([]);
  });
});
