# Schema Namespace Conventions

This repository defines core protocol schemas in `*.spore` files. The generated
manifest, static schema fragment, and TypeScript clients all use the following
namespace rules.

## Core principle

All **core protocol schemas** (sporemind, gospore, and spore itself) live in the
`system` namespace. This keeps wire IDs stable (schema IDs themselves do not
change, only the namespace offset they belong to) and eliminates duplicate schema
entries in the generated manifest.

## Namespace usage

| Namespace | Purpose |
|-----------|---------|
| `system`  | Core protocol structs used across actors and services. This is the default namespace for new schema definitions. |
| actor / service namespace (e.g. `agent`, `workspace`, `filesystem`) | Only `callable`, `projection`, and `event` definitions live here. They provide routing and remain in the namespace of the actor/service that exposes them. |
| plugin / third-party namespace | External plugins or third-party integrations may use their own namespaces for both schemas and callables. |

## Generated code layout

After running `make gen-ts`:

- `web/src/gen-clients/system/types.ts` contains every core protocol schema.
- Other namespace directories (e.g. `web/src/gen-clients/agent/types.ts`) are
  only emitted when that namespace actually owns a schema. With core schema
  normalization, most directories have only `callables.ts`, `client.ts`,
  `registry.ts`, and `projection-client.ts`.
- Cross-namespace references in generated code are emitted as qualified imports:

  ```ts
  import type * as systemTypes from "../system/types";
  export function foo(req: systemTypes.FooReq): systemTypes.FooResp { ... }
  ```

## Adding new schemas or callables

- New struct schema definitions should be added to the `system` namespace.
- New actor/service callables should still be added in the actor/service's own
  namespace for routing.
- If a new callable needs to reference a struct, reference the `system` schema by
  name or schema ID; the manifest generator and TS renderers will resolve it
  across namespaces.

## Schema id segments

Each `*.spore` file assigns struct ids from its `._N` filename base plus
declaration-order offset (explicit `@schema(N)` annotations are ignored). When
a module's reserved space overflows, run `make renumber-schemas`: it reassigns
the `._N` bases so every module (the stem with any `.partN` suffix stripped,
e.g. `aigen.part1`…`part4` all belong to `aigen`) owns one contiguous segment
sized to its struct count plus a gap of `max(32, ⌈count/2⌉)` rounded to 16.
Follow it with `make gen`. Renumbering changes wire ids, so persisted archives
written with old ids are not compatible.

## Manifest normalization

`sporemind-gen-manifest` always normalizes exported schema entries to `system` and deduplicates by
`schemaId`. The `/debug/schema` endpoint applies the same normalization so that
runtime responses match the committed manifest.
