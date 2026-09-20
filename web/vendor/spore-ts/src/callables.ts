/**
 * CallableRegistry — runtime lookup table for callable invocation metadata.
 *
 * Mirrors SchemaRegistry but for callables: codegen emits one
 * `<namespace>/callables.ts` file per namespace whose entries are appended
 * here at startup. Consumers use the registry to translate between
 * callable identity ((namespace, name) or (namespace, reqSchemaId)) and the
 * wire schema IDs / TypeDescs needed to encode requests, decode chunks, and
 * route inbound frames.
 *
 * The shape intentionally parallels the Go side `CallableSpec` in
 * exp10 — once Spore's host runtime ships, the registry will be the single
 * source of truth for routing inbound stream frames to the right
 * client-side push-stream handle.
 */

import type { CallableMode, TypeDesc } from "./schema.js";

/** One callable's invocation metadata. */
export interface CallableEntry {
  namespace: string;
  name: string;
  visibility?: string;
  mode: CallableMode;
  reqSchemaId: number;
  /** Set only for streaming callables; omitted for unary. */
  chunkSchemaId?: number;
  finalSchemaId: number;
  req: TypeDesc;
  /** Set only for streaming callables; omitted for unary. */
  chunk?: TypeDesc;
  final: TypeDesc;
}

/**
 * A callable registry resolves callable identity to its CallableEntry.
 * Keys: (namespace, name) for ergonomic lookup, (namespace, reqSchemaId) for
 * wire-frame routing. Both views must stay consistent — register() throws
 * on either kind of collision.
 */
export class CallableRegistry {
  private byName = new Map<string, CallableEntry>();
  private byReqSchema = new Map<string, CallableEntry>();

  /** Register a callable. Throws on duplicate (namespace, name) or
   *  (namespace, reqSchemaId). */
  register(entry: CallableEntry): void {
    const nameKey = keyOfName(entry.namespace, entry.name);
    if (this.byName.has(nameKey)) {
      throw new Error(`spore-ts: callable name collision: ${nameKey}`);
    }
    const idKey = keyOfId(entry.namespace, entry.reqSchemaId);
    if (this.byReqSchema.has(idKey)) {
      throw new Error(`spore-ts: callable reqSchemaId collision: ${idKey}`);
    }
    this.byName.set(nameKey, entry);
    this.byReqSchema.set(idKey, entry);
  }

  /** Retrieve a callable by (namespace, name). */
  get(namespace: string, name: string): CallableEntry | undefined {
    return this.byName.get(keyOfName(namespace, name));
  }

  /** Retrieve a callable by (namespace, reqSchemaId) — used for routing
   *  inbound Call frames on the server side. */
  getByReqSchema(namespace: string, reqSchemaId: number): CallableEntry | undefined {
    return this.byReqSchema.get(keyOfId(namespace, reqSchemaId));
  }

  /** Snapshot of registered namespaces. */
  namespaces(): string[] {
    const set = new Set<string>();
    for (const entry of this.byName.values()) set.add(entry.namespace);
    return [...set].sort();
  }

  /** Snapshot of (namespace, name) pairs registered. Useful for
   *  enumerating client-side method bindings. */
  list(): { namespace: string; name: string }[] {
    return [...this.byName.values()].map((e) => ({ namespace: e.namespace, name: e.name }));
  }
}

function keyOfName(ns: string, name: string): string {
  return `${ns}#${name}`;
}

function keyOfId(ns: string, id: number): string {
  return `${ns}/${id}`;
}
