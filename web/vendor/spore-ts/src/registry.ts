/**
 * SchemaRegistry — a runtime lookup table mapping `(namespace, schemaId)`
 * pairs to ObjectDesc + TypeDesc descriptors.
 *
 * Built either by hand (for tests) or by importing the registry files
 * produced by `spore-gen-ts`. Consumers feed the registry's lookup
 * function into JSONCodec so struct values can be checked against their
 * shape.
 */

import type { ObjectDesc, TypeDesc } from "./schema.js";

/** Metadata associated with one registered schema entry. */
export interface SchemaEntry {
  namespace: string;
  schemaId: number;
  name: string;
  visibility?: string;
  type: TypeDesc;
  /** Object shape, if the entry describes a struct or class. */
  object?: ObjectDesc;
}

/**
 * A schema registry resolves `(schemaNamespace, schemaId)` and `(schemaNamespace, name)`
 * into SchemaEntry instances. schemaNamespace is the namespace the schema is registered under (e.g. "system"), not the callable's route namespace. The default in-memory implementation is plain;
 * consumers can plug in versioned or persistent backings.
 */
export class SchemaRegistry {
  private byId = new Map<string, SchemaEntry>();
  private byName = new Map<string, SchemaEntry>();

  /** Register a schema. Throws on duplicate (namespace, schemaId) or (namespace, name). */
  register(entry: SchemaEntry): void {
    const idKey = keyOfId(entry.namespace, entry.schemaId);
    if (this.byId.has(idKey)) {
      throw new Error(`spore-ts: schema id collision: ${idKey}`);
    }
    const nameKey = keyOfName(entry.namespace, entry.name);
    if (this.byName.has(nameKey)) {
      throw new Error(`spore-ts: schema name collision: ${nameKey}`);
    }
    this.byId.set(idKey, entry);
    this.byName.set(nameKey, entry);
  }

  /** Retrieve a schema by (namespace, schemaId). */
  get(schemaNamespace: string, schemaId: number): SchemaEntry | undefined {
    return this.byId.get(keyOfId(schemaNamespace, schemaId))
      ?? this.byId.get(keyOfId("system", schemaId));
  }

  /** Retrieve a schema by (namespace, name). */
  getByName(schemaNamespace: string, name: string): SchemaEntry | undefined {
    return this.byName.get(keyOfName(schemaNamespace, name))
      ?? this.byName.get(keyOfName("system", name));
  }

  /** Convenience: build an `objectFor(name)` resolver bound to this registry. */
  objectFor(schemaNamespace: string): (name: string) => ObjectDesc | undefined {
    return (name: string) => this.getByName(schemaNamespace, name)?.object;
  }

  /** Retrieve a schema by ID alone, searching across all namespaces. */
  getById(schemaId: number): SchemaEntry | undefined {
    for (const entry of this.byId.values()) {
      if (entry.schemaId === schemaId) return entry;
    }
    return undefined;
  }

  /** Snapshot of registered namespaces. */
  namespaces(): string[] {
    const set = new Set<string>();
    for (const entry of this.byId.values()) set.add(entry.namespace);
    return [...set].sort();
  }
}

function keyOfId(ns: string, id: number): string {
  return `${ns}/${id}`;
}

function keyOfName(ns: string, name: string): string {
  return `${ns}#${name}`;
}
