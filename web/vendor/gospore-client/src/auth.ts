import type { InvokeOptions, Transport, ManagedTransport } from "./transport";

/**
 * AuthProvider supplies tokens on demand.
 * GosporeClient calls getToken() before every invoke/subscribe.
 */
export interface AuthProvider {
  /** Return the current auth token (e.g. sessionId, JWT). */
  getToken(): Promise<string> | string;

  /** Called when the server rejects a request as unauthenticated.
   *  The provider may refresh the token or redirect to login.
   */
  onAuthFailure?(): void;
}

export interface AuthedTransport extends Transport {
  readonly auth: AuthProvider;
}

export interface ManagedAuthedTransport extends AuthedTransport {
  connect(): void;
  waitUntilReady(): Promise<void>;
  reconnect(): void;
  onConnected(handler: (info: { isReconnect: boolean }) => void): () => void;
}

/**
 * Wraps a Transport to inject auth tokens into every request.
 *
 * DEPRECATED: Auth for WebSocket is now handled at the transport level
 * via WebSocketTransportOptions.getUrl. This wrapper is kept for API
 * compatibility but no longer mutates payloads.
 */
export function withAuth(transport: ManagedTransport, auth: AuthProvider): ManagedAuthedTransport;
export function withAuth(transport: Transport, auth: AuthProvider): AuthedTransport;
export function withAuth(transport: Transport, auth: AuthProvider): AuthedTransport {
  return new AuthInterceptor(transport, auth);
}

class AuthInterceptor implements AuthedTransport {
  constructor(private inner: Transport, readonly auth: AuthProvider) {}

  async invoke(callID: string, payload: unknown, opts?: InvokeOptions): Promise<unknown> {
    try {
      return await this.inner.invoke(callID, payload, opts);
    } catch (err) {
      if (isAuthError(err)) this.auth.onAuthFailure?.();
      throw err;
    }
  }

  async *subscribe(callID: string, payload: unknown, opts?: InvokeOptions): AsyncIterable<unknown> {
    try {
      for await (const chunk of this.inner.subscribe(callID, payload, opts)) {
        yield chunk;
      }
    } catch (err) {
      if (isAuthError(err)) this.auth.onAuthFailure?.();
      throw err;
    }
  }

  close(): void {
    this.inner.close();
  }

  connect(): void {
    const transport = this.inner as ManagedTransport;
    if (typeof transport.connect === "function") transport.connect();
  }

  waitUntilReady(): Promise<void> {
    const transport = this.inner as ManagedTransport;
    if (typeof transport.waitUntilReady === "function") return transport.waitUntilReady();
    return Promise.resolve();
  }

  reconnect(): void {
    const transport = this.inner as ManagedTransport;
    if (typeof transport.reconnect === "function") transport.reconnect();
  }

  onConnected(handler: (info: { isReconnect: boolean }) => void): () => void {
    const transport = this.inner as ManagedTransport;
    if (typeof transport.onConnected === "function") return transport.onConnected(handler);
    return () => {};
  }
}

function isAuthError(err: unknown): boolean {
  if (err instanceof Error) {
    const msg = err.message.toLowerCase();
    return msg.includes("unauthorized") || msg.includes("unauthenticated") || msg.includes("401") || msg.includes("403");
  }
  return false;
}
