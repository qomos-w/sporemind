import type { InvokeOptions, Transport, ManagedTransport } from "./transport";
import type { WithSeqNo } from "./channel";
import type { AuthProvider } from "./auth";
import { withAuth } from "./auth";

export interface GosporeClientOptions {
  /** Optional auth provider. When set, tokens are injected into every call. */
  auth?: AuthProvider;
}

/** Cancel function returned by GosporeClient.events.onService/onInstance. Idempotent. */
export type EventCancel = () => void;

/**
 * EventsAPI exposes the App-scoped event bus. Two subscription modes:
 *
 * - onService(serviceName, kind, handler) — subscribe to events from a
 *   service-registered actor (e.g. "workspace", "filesystem").
 *
 * - onInstance(actorId, kind, handler) — subscribe to events from a
 *   specific actor instance identified by its canonical actor ID.
 */
export interface EventsAPI {
  /** Subscribe to events from a service-registered actor. */
  onService(serviceName: string, kind: string, handler: (payload: unknown) => void): EventCancel;
  /** Subscribe to events from a specific actor instance by its canonical ID. */
  onInstance(actorId: string, kind: string, handler: (payload: unknown) => void): EventCancel;
}

/**
 * GosporeClient is the typed entry point for calling gospore actors.
 *
 * Usage:
 *   const client = new GosporeClient(new HTTPTransport("/api"));
 *   const res = await client.invoke<MountReq, ProjectRef>("workspace.mount", req);
 */
export class GosporeClient {
  private transport: Transport;

  /** App-scoped event bus subscriber surface. */
  readonly events: EventsAPI;

  constructor(transport: Transport, options?: GosporeClientOptions) {
    this.transport = options?.auth ? withAuth(transport, options.auth) : transport;
    this.events = makeEventsAPI(this.transport);
  }

  /** Invoke a unary callable. */
  async invoke<TReq, TRes>(callID: string, req: TReq, opts?: InvokeOptions): Promise<TRes> {
    return this.transport.invoke(callID, req, opts) as Promise<TRes>;
  }

  /** Subscribe to a streaming callable.
   *
   * When opts.withSeqNo is true, yields {seqNo, payload} objects.
   * Overload signatures provide correct typing for both modes. */
  subscribe<TChunk>(callID: string, req: unknown, opts?: InvokeOptions): AsyncIterable<TChunk>;
  subscribe<TChunk>(callID: string, req: unknown, opts?: InvokeOptions & { withSeqNo: true }): AsyncIterable<WithSeqNo<TChunk>>;
  async *subscribe(callID: string, req: unknown, opts?: InvokeOptions): AsyncIterable<any> {
    for await (const chunk of this.transport.subscribe(callID, req, opts)) {
      yield chunk;
    }
  }

  /** Close the underlying transport. */
  close(): void {
    this.transport.close();
  }

  /** Access the underlying transport (e.g. for reauth). */
  getTransport(): Transport {
    return this.transport;
  }

  /** Initiate a connection. No-op when the transport does not support lifecycle. */
  connect(): void {
    const mt = this.transport as ManagedTransport;
    if (typeof mt.connect === "function") mt.connect();
  }

  /** Resolves when the transport is connected and ready. Returns immediately
   *  for transports that do not implement ManagedTransport. */
  waitUntilReady(): Promise<void> {
    const mt = this.transport as ManagedTransport;
    if (typeof mt.waitUntilReady === "function") return mt.waitUntilReady();
    return Promise.resolve();
  }

  /** Force a reconnection. No-op when the transport does not support lifecycle. */
  reconnect(): void {
    const mt = this.transport as ManagedTransport;
    if (typeof mt.reconnect === "function") mt.reconnect();
  }

  /** Register a connection-state callback. Returns an idempotent unsubscribe.
   *  For transports that do not support lifecycle the callback is never called. */
  onConnected(handler: (info: { isReconnect: boolean }) => void): () => void {
    const mt = this.transport as ManagedTransport;
    if (typeof mt.onConnected === "function") return mt.onConnected(handler);
    return () => {};
  }
}

const eventSubscribeServiceCallID = "gospore.events.subscribe_service";
const eventSubscribeInstanceCallID = "gospore.events.subscribe_instance";

interface SharedEventSubscription {
  handlers: Map<number, (payload: unknown) => void>;
  iter: AsyncIterator<unknown>;
  cancelled: boolean;
  nextHandlerID: number;
}

type EventBucketKey = string;

function makeEventsAPI(transport: Transport): EventsAPI {
  const shared = new Map<EventBucketKey, SharedEventSubscription>();

  return {
    onService(serviceName: string, kind: string, handler: (payload: unknown) => void): EventCancel {
      const key = `service:${serviceName}\0${kind}`;
      console.log(`[gospore-client] onService subscribe: serviceName=${serviceName}, kind=${kind}`);
      return subscribeEvents(transport, shared, key, eventSubscribeServiceCallID, { serviceName, kind }, serviceName, kind, handler);
    },

    onInstance(actorId: string, kind: string, handler: (payload: unknown) => void): EventCancel {
      const key = `instance:${actorId}\0${kind}`;
      console.log(`[gospore-client] onInstance subscribe: actorId=${actorId}, kind=${kind}`);
      return subscribeEvents(transport, shared, key, eventSubscribeInstanceCallID, { actorId, kind }, actorId, kind, handler);
    },
  };
}

function subscribeEvents(
  transport: Transport,
  shared: Map<EventBucketKey, SharedEventSubscription>,
  key: string,
  callID: string,
  req: unknown,
  label: string,
  kind: string,
  handler: (payload: unknown) => void,
): EventCancel {
  let bucket = shared.get(key);

  if (!bucket) {
    const iterable = transport.subscribe(callID, req);
    const iter = iterable[Symbol.asyncIterator]() as AsyncIterator<unknown>;
    bucket = {
      handlers: new Map<number, (payload: unknown) => void>(),
      iter,
      cancelled: false,
      nextHandlerID: 1,
    };
    shared.set(key, bucket);

    void (async () => {
      try {
        while (!bucket!.cancelled) {
          const result = await bucket!.iter.next();
          if (result.done || bucket!.cancelled) break;
          console.log(`[gospore-client] subscribeEvents received chunk: label=${label}, kind=${kind}, handlers=${bucket!.handlers.size}`);
          for (const activeHandler of bucket!.handlers.values()) {
            try {
              activeHandler(result.value);
            } catch (err) {
              console.error(`gospore.events(${label}, ${kind}) handler threw:`, err);
            }
          }
          // Yield to the macrotask queue so React can flush renders between
          // events. Without this, a burst of queued chunks is processed in a
          // single microtask and the UI renders once with the final state.
          await new Promise<void>(r => setTimeout(r, 0));
        }
      } catch (err) {
        console.error(`gospore.events(${label}, ${kind}) subscription ended with error:`, err);
      } finally {
        bucket!.cancelled = true;
        // Revival guard: between this pump's cancel and its async exit, a new
        // mount may have created a replacement bucket under the same key.
        // Deleting unconditionally would orphan the replacement — its cancel
        // closure looks the bucket up in `shared` and would silently no-op,
        // leaking the backend stream (and the handler closures) forever.
        if (shared.get(key) === bucket) {
          shared.delete(key);
        }
        await bucket!.iter.return?.();
      }
    })();
  }

  const handlerID = bucket.nextHandlerID++;
  bucket.handlers.set(handlerID, handler);

  return () => {
    const current = shared.get(key);
    if (!current) return;
    current.handlers.delete(handlerID);
    if (current.handlers.size === 0 && !current.cancelled) {
      current.cancelled = true;
      void current.iter.return?.();
      shared.delete(key);
    }
  };
}
