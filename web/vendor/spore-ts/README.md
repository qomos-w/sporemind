# @qomos/spore-ts

TypeScript runtime for the spore protocol layer. Mirrors the Go-side
`spore/schema`, `spore/identity`, and `spore/transport` packages.

This package contains **only** the protocol primitives: schema descriptors,
identity, JSON codec, and a small runtime registry. It deliberately does
**not** include any actor / orchestration / RPC concepts — those live in
`@qomos/gospore-ts`.

## Install

```bash
npm install @qomos/spore-ts
```

Requires Node 18+ (uses `TextDecoder`, `TextEncoder`, native `bigint`).

## Modules

### Scope / vocabulary note

This package is the **TypeScript protocol mirror** of Spore's public schema /
identity / codec surface. It does **not** execute script callables, manage
stream sessions, or define host runtime binding authority.

Key terms in this package:
- `ObjectDesc` = serialized shape descriptor container
- `TypeDesc` = type reference descriptor
- `kind: "struct"` = current wire/type vocabulary for export-safe data shapes
- `SchemaRegistry` = `(namespace, schemaId)` lookup table for schema entries

If you need host invocation semantics (`Call`, `CallNext`, `CallFinal`) or
stream lifecycle behavior, those belong to Spore's Go `script.Runtime`
surface, not this package.

| Path | Contents |
|---|---|
| `@qomos/spore-ts` | Re-exports everything below |
| `@qomos/spore-ts/schema` | `TypeDesc`, `ObjectDesc`, `FieldDesc`, `CallableDesc`, … |
| `@qomos/spore-ts/identity` | `CanonicalID` 16-byte form + hex round-trip |
| `@qomos/spore-ts/codec` | `JSONCodec`, `EncodeError`, `DecodeError` |
| `@qomos/spore-ts/registry` | `SchemaRegistry`, `SchemaEntry` |

## Quick example

```ts
import {
  JSONCodec,
  SchemaRegistry,
  type ObjectDesc,
  type TypeDesc,
} from "@qomos/spore-ts";

const userObject: ObjectDesc = {
  kind: "struct",
  name: "User",
  fields: [
    { name: "ID",  type: { kind: "scalar", name: "string" } },
    { name: "Age", type: { kind: "scalar", name: "int" } },
  ],
};

const userTypeRef: TypeDesc = {
  kind: "struct",
  name: "User",
  className: "User",
};

const registry = new SchemaRegistry();
registry.register({
  namespace: "demo",
  schemaId: 1,
  name: "User",
  type: userTypeRef,
  object: userObject,
});

const codec = new JSONCodec();
const bytes = codec.encode(
  userTypeRef,
  { ID: "alice", Age: 30 },
  registry.objectFor("demo"),
);
const back = codec.decode(userTypeRef, bytes, registry.objectFor("demo"));
console.log(back); // { Age: 30, ID: "alice" } — fields ordered alphabetically
```

In production the schemas come from `spore-gen-ts` codegen rather than
hand-built ObjectDesc literals; generated registry modules expose
`buildSchemaRegistry()` for direct runtime use. See the spore repo
`cmd/spore-gen-ts/README.md`.

## Build / develop

```bash
npm install
npm test         # runs vitest
npm run build    # tsc → dist/ + .d.ts
```

The package ships **ESM only**. There is no CommonJS build; consumers
must use Node 18+ or a modern bundler (Vite, Webpack 5, esbuild, etc.).

## Stability

spore-ts mirrors the Go protocol surface. **Breaking changes happen
only when spore Go side breaks** — the goal is `(namespace, schemaId)`
stays a stable cross-process key forever, and the JSON wire format stays
deterministic (struct fields and map keys sorted alphabetically) so two
clients can compute identical fingerprints for identical content.

## What's not here

- `BinaryCodec` — comes when spore ships its binary wire format
- `Frame` / wire transport — that lives in `@qomos/gospore-ts`
- Actor / Invoke / Capability concepts — gospore concerns
- Host runtime callable invocation (`Call`, `CallNext`, `CallFinal`) — belongs to Spore's Go `script.Runtime`
- Stream session lifecycle / cancellation semantics — host runtime concerns, not part of this TS package
- Class / interface / callable codegen emission — MVP focuses on export-safe struct/data shapes

## License

MIT
