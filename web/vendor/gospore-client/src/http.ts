/**
 * HTTPTransport uses fetch for unary calls and EventSource for subscriptions.
 *
 * Frame format matches gospore's JSON codec:
 *   invoke    → POST /api/{callID}[?target=...] with body = payload
 *   subscribe → SSE stream on GET?callID=...[&target=...]
 *
 * EventSource limitation: only GET, no body, no custom headers.
 * Payload (including auth token) is serialized into the query string.
 *
 * InvokeOptions.target — if set, attached as ?target=... so the gateway
 * routes by ActorID rather than the leading-segment service.
 */

import type { InvokeOptions, Transport } from "./transport";

export interface HTTPTransportOptions {
  /** Base URL for all requests (e.g. "/api/v0"). */
  baseURL: string;

  /** Optional headers added to every request. */
  headers?: Record<string, string>;

  /** How long to wait for an invoke response before aborting. Default 6s. */
  timeoutMs?: number;
}

export class HTTPTransport implements Transport {
  private baseURL: string;
  private headers: Record<string, string>;
  private timeoutMs: number;
  private sources = new Set<EventSource>();

  constructor(options: HTTPTransportOptions) {
    this.baseURL = options.baseURL.replace(/\/$/, "");
    this.headers = options.headers ?? {};
    this.timeoutMs = options.timeoutMs ?? 6000;
  }

  async invoke(callID: string, payload: unknown, opts?: InvokeOptions): Promise<unknown> {
    const url = `${this.baseURL}/invoke`;
    // eslint-disable-next-line no-console
    console.log(`[HTTPTransport] invoke ${callID} → ${url}`, { headers: this.headers, payload, target: opts?.target });
    const timeoutMs = opts?.timeoutMs ?? this.timeoutMs;
    const ctrl = new AbortController();
    const timer = setTimeout(
      () => ctrl.abort(new Error(`HTTPTransport: invoke ${callID} timed out after ${timeoutMs}ms`)),
      timeoutMs,
    );

    try {
      const body: Record<string, unknown> = { callID, payload };
      if (opts?.target) body.target = opts.target;
      if (opts?.timeoutMs) body.timeoutMs = opts.timeoutMs;
      const res = await fetch(url, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          ...this.headers,
        },
        body: JSON.stringify(body),
        signal: ctrl.signal,
      });
      clearTimeout(timer);

      if (!res.ok) {
        const text = await res.text().catch(() => "");
        // eslint-disable-next-line no-console
        console.error(`[HTTPTransport] invoke ${callID} HTTP error: ${res.status} ${text}`);
        throw new Error(`invoke ${callID} failed: ${res.status} ${text}`);
      }

      const text = await res.text();
      // eslint-disable-next-line no-console
      console.log(`[HTTPTransport] invoke ${callID} ← ${text.length} bytes`);
      if (text === "") return null;
      // gospore encodes string/[]byte returns as raw passthrough, not JSON.
      // Try JSON first (structured responses), fall back to raw text.
      try {
        return JSON.parse(text);
      } catch {
        return text;
      }
    } catch (err) {
      clearTimeout(timer);
      // eslint-disable-next-line no-console
      console.error(`[HTTPTransport] invoke ${callID} fetch error:`, err);
      if (err instanceof Error && err.name === "AbortError" && ctrl.signal.reason instanceof Error) {
        throw ctrl.signal.reason;
      }
      throw err;
    }
  }

  async *subscribe(callID: string, payload: unknown, opts?: InvokeOptions): AsyncIterable<unknown> {
    const params = new URLSearchParams();
    params.set("callID", callID);
    if (payload !== undefined && payload !== null) {
      params.set("payload", JSON.stringify(payload));
    }
    if (opts?.target) {
      params.set("target", opts.target);
    }

    const url = `${this.baseURL}/subscribe?${params.toString()}`;
    const es = new EventSource(url);
    this.sources.add(es);

    const queue: unknown[] = [];
    let done = false;
    let error: Error | null = null;

    es.onmessage = (ev) => {
      try {
        const data = JSON.parse(ev.data);
        // gospore gateway sends {"done":true} as an EOF marker; swallow it
        // and close the stream gracefully.
        if (data && typeof data === "object" && data.done === true) {
          done = true;
          es.close();
          return;
        }
        queue.push(data);
      } catch (err) {
        error = err instanceof Error ? err : new Error(String(err));
        es.close();
      }
    };

    es.onerror = () => {
      // Normal close after EOF marker fires onerror; don't treat it as fatal.
      if (!done) {
        error = new Error("SSE connection lost");
      }
      es.close();
    };

    const cleanup = () => {
      done = true;
      es.close();
      this.sources.delete(es);
    };

    try {
      while (!done || queue.length > 0) {
        if (error) throw error;
        if (queue.length > 0) {
          yield queue.shift()!;
          continue;
        }
        await sleep(10);
      }
    } finally {
      cleanup();
    }
  }

  close(): void {
    for (const es of this.sources) {
      es.close();
    }
    this.sources.clear();
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
