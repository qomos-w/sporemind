/**
 * Transport is the low-level wire abstraction.
 * GosporeClient wraps a Transport and adds typing + auth.
 */

/**
 * InvokeOptions carries optional per-call routing metadata that travels
 * with both invoke and subscribe.
 *
 * `target` — gospore ActorID (canonical 32-char lowercase hex). When set
 * the gateway routes the call to that specific actor instance instead of
 * resolving by the leading-segment service. Used to address per-instance
 * actors such as a particular `agent`.
 */
export interface InvokeOptions {
  target?: string;
  /** When true, subscribe() yields {seqNo, payload} objects instead of raw payload. */
  withSeqNo?: boolean;
  /** Request schema ID for binary codec encoding. */
  reqSchemaId?: number;
  /** Response schema ID for binary codec decoding. */
  resSchemaId?: number;
  /** Chunk schema ID for binary codec decoding of streaming responses. */
  chunkSchemaId?: number;
  /** Per-call timeout in milliseconds. Overrides transport default. */
  timeoutMs?: number;
}

export interface Transport {
  /** Invoke a unary callable. */
  invoke(callID: string, payload: unknown, opts?: InvokeOptions): Promise<unknown>;

  /** Subscribe to a streaming callable. */
  subscribe(callID: string, payload: unknown, opts?: InvokeOptions): AsyncIterable<unknown>;

  /** Close the underlying connection. Idempotent. */
  close(): void;
}

/**
 * ManagedTransport extends Transport with explicit lifecycle management:
 * connect, waitUntilReady, reconnect, and connection-state notifications.
 *
 * WebSocketTransport implements this interface. Simpler transports such as
 * HTTPTransport implement only Transport and are wrapped by GosporeClient
 * with no-op defaults for lifecycle methods.
 */
export interface ManagedTransport extends Transport {
  /** Initiate a connection. Safe to call multiple times. */
  connect(): void;

  /** Resolves when the transport is connected and authenticated (if applicable).
   *  Rejects on failure or if the transport is closed before becoming ready. */
  waitUntilReady(): Promise<void>;

  /** Force a reconnection. For transports with automatic reconnect logic
   *  this schedules an immediate reconnect attempt. */
  reconnect(): void;

  /** Register a callback that fires each time the transport establishes
   *  (or re-establishes) a connection. Returns an idempotent unsubscribe. */
  onConnected(handler: (info: { isReconnect: boolean }) => void): () => void;
}
