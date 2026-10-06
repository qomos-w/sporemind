# sporescript language reference

This document is the reference for the sporescript language: lexical
structure, types, declarations, statements, expressions, closures, streaming
callables, and host interop.

[简体中文](SYNTAX.zh-CN.md)

sporescript is a statically typed language that compiles to stack-based
bytecode and runs on an embedded VM. It is designed for host embedding: every
declaration maps onto schema descriptors, callable descriptors, or host
bindings, and every value shape is transport-projectable.

---

## 1. Lexical structure

- Identifiers: letters, digits, `_`; may not start with a digit.
- Comments: line comments `//` and block comments `/* ... */`.
- Numeric literals are decimal only. There are **no numeric suffixes**
  (`1L`, `1.5f` do not exist); the declared target type decides the encoding
  width (see §7).
- String literals use double quotes; `bytes` values are a distinct type.
- Keywords are reserved and may not be used as identifiers.

### Keywords

```
fun export class struct enum type var package import
void any bool byte short ushort uint long ulong double bytes
array map int float string media
return if else for while in when case break continue
is as from this new true false null super constructor
open override interface public private static
stream yield async await optional key try catch defer
```

## 2. Types

### 2.1 Scalars and special types

| Type | Meaning |
|------|---------|
| `bool` | boolean |
| `byte` `short` `ushort` `int` `uint` `long` `ulong` | fixed-width integers (8/16/32/64 bit, signed/unsigned) |
| `float` `double` | IEEE-754 32/64 bit |
| `string` | UTF-8 string |
| `bytes` | byte sequence |
| `any` | type-erased value |
| `void` | no value (return position only) |
| `null` | null literal / nullable absence |
| `media` | host media handle |

### 2.2 Arrays and ordered maps

```spore
var xs: int[] = [1, 2, 3]
var m: map<string, int> = {"a": 1, "b": 2}
```

`array<T>` is the long form of `T[]`. Maps are an **ordered map** surface:
lookup `m["key"]`, set `m["key"] = v`, `delete(m, "key")`, `len(m)`,
enumeration, and snapshot/diff all observe a stable entry order. Map literal
keys are strings.

### 2.3 Struct — value surface

`struct` is the schema-friendly value shape: named fields, pass-by-value,
easy to describe, diff, and transport.

```spore
struct Point {
    x: int
    y: int
}

var p: Point = Point{x: 10, y: 20}
```

**Struct literals require every field.** To let the remaining fields take
their type's zero value, write a trailing `..` — an explicit opt-in:

```spore
var p: Point = Point{..}            // x=0, y=0
var q: Point = Point{x: 7, ..}      // x=7, y=0
```

`..` may appear at most once and only at the end. Omitting a field without
`..` is the compile error `missing_struct_field`.

**Declaration decorators:**

```spore
@schema(3)
@component
struct Event {
    id: string
    optional payload: string
}
```

- `@schema(N)` — declares the transport schema id (single integer literal,
  `struct` declarations only). Carried into `ObjectDesc.SchemaID`.
- `@component` — marks the struct as an ECS component (`ObjectDesc.IsComponent`).
  A component must carry a schema id; a schema-carrying struct is not
  automatically a component. May combine with `@schema(N)` in either order.
- `optional` — field modifier on `struct` and `class` fields marking the field
  optional in the descriptor (`FieldDesc.Optional`). It is schema metadata;
  it does **not** relax the literal-completeness rule. A field literally named
  `optional` (`optional: T`) still parses as a field name.

### 2.3.1 `@data` — keyed data-table rows

`@data` declares a struct whose instances are rows of a keyed data table
(startup-loaded configuration), not ECS components. It is the third struct
role alongside plain schema structs and `@component`:

```spore
@data
@version(2)
struct AnimalType {
    key id: string
    name: string
    biome: BiomeKind
    @ref(AnimalType) optional evolvesInto: string
}
```

- `@data` — takes no arguments. The struct never receives a transport schema
  id: it must not combine with `@schema(N)` or `@component` (both are errors).
  Code generators emit it as a plain struct — no component descriptor, no
  world registry entry — plus, when requested, a data-table manifest.
- `@version(N)` — optional, `@data` structs only. A consumer-side format
  version for forward-compat gating of loaded data (`ObjectDesc.DataVersion`,
  0 = unversioned). Independent from transport schema ids.
- `key` — field modifier (same reserved-word and `key: T` disambiguation
  rules as `optional`) marking the table's key field. Exactly one per
  `@data` struct; not optional; type must be `string`, an integer scalar
  (`byte short ushort int uint long ulong`), or an enum. Marked `key`/`@ref`
  fields inside a non-`@data` struct are an error.
- `@ref(T)` / `@ref(T.field)` — field decorator declaring a foreign key into
  the `@data` struct `T`; `@ref(T)` targets T's key field, `@ref(T.field)`
  an explicit field of T. The field must be `string` or `array<string>`
  (arrays reference every element) and T must be string-keyed. Composable
  with `optional` (nullable reference); decorators precede modifiers
  (`@ref(T) optional boss: string`). Inside one struct the rules are
  enforced at codegen time; the parser itself only checks decorator
  spelling and argument shape.

### 2.4 Class — object surface

`class` defines objects with fields, methods, constructors, inheritance, and
override dispatch:

```spore
class User {
    id: string
    name: string

    fun displayName(): string {
        return name
    }
}
```

Implemented class machinery: `this`, `new`, `constructor`, `super.method()`,
`super()` constructor chaining, `open`, `override`, inheritance
(`class Person : Greeter`). Class fields support the same `optional` modifier
as struct fields.

Structs are the preferred **export-safe carrier**; classes are the object and
binding surface. An exported struct graph may not reach a class type — such an
export is rejected.

### 2.5 Interface

Interfaces declare method signatures and may carry **default bodies**:

```spore
interface Greeter {
    fun greet(): string { return "hello" }   // default implementation
    fun name(): string                        // abstract: class must provide
}

class Person : Greeter {
    fun name(): string { return "alice" }
    // greet() inherited from the interface default
}

class Robot : Greeter {
    fun greet(): string { return "beep" }     // overrides the default
    fun name(): string { return "r2" }
}
```

Default bodies follow ordinary method-body rules and dispatch calls on `this`
dynamically, so a default can call other interface methods the implementing
class provides. A class providing neither the method nor an inherited
implementation for an abstract interface method fails with
`interface_method_missing`.

### 2.6 Type aliases

```spore
type UserID = string
```

Aliases name schema contracts; they do not introduce a new runtime
representation.

### 2.7 Function types

`fun(P1, P2): R` in type position. The return type after a function type's
closing `)` belongs to the function type; a callable's own return annotation
follows its parameter list, so the two never conflict:

```spore
fun apply(cb: fun(int): int): int {
    return cb(1)
}
```

## 3. Declarations

### 3.1 `fun`, `export fun`

```spore
fun add(a: int, b: int): int {
    return a + b
}

export fun greet(name: string): string {
    return "hello, " + name
}
```

`export fun` declares a callable that **may** participate in external
binding; actual exposure happens only through an explicit host-side
binding/registration step.

### 3.2 `stream fun` — streaming callables

```spore
stream fun chat(prompt: string): MessageStreamEvent {
    yield MessageStreamEvent{kind: "delta"}
    return MessageStreamEvent{kind: "end"}
}
```

See §8 for the full streaming contract.

### 3.3 `var`

```spore
var count: int = 42
```

Inside bodies `var` is an ordinary local binding. At top level it declares
global configuration/state; loading a module does not execute arbitrary
top-level logic.

### 3.4 `package`

A module declares its name with `package name;`-style header syntax. Module
linking semantics are explicit import/export (§9).

## 4. Statements and control flow

| Statement | Form |
|-----------|------|
| `if / else` | `if cond { ... } else { ... }` |
| `when / case` | see below |
| `for in` | `for x in xs { ... }` |
| `while` | `while cond { ... }` |
| `return` | `return expr` / `return` |
| `break` / `continue` | loop control |
| `try / catch` | `try { ... } catch (e) { ... }` |
| `defer` | `defer { ... }` |
| `yield` | streaming callables only (§8) |

`when / case` supports value matching, type matching with binding, guards,
and an optional `else` branch:

```spore
when classify(v) {
    case 1, 2, 3 { return "low" }
    case x: int when x > 100 { return "big" }
    case s: string { return "text" }
    else { return "other" }
}
```

A `case ... when guard` whose guard evaluates to false falls through to the
next case (or `else`).

## 5. Expressions

| Category | Operators / forms |
|----------|-------------------|
| member access | `.` |
| optional chaining | `x?.field`, `x?.method(args)` — null receiver short-circuits the whole chain to `null` |
| null coalescing | `x ?? y` — `y` only when `x` is null (false/0/"" are not null); left-associative, binds looser than `\|\|` |
| calls | `f(args)` |
| indexing | `a[i]`, `m["key"]` |
| arithmetic | `+ - * / %` |
| comparison | `== != < <= > >=` |
| logic | `&& \|\| !` |
| type check / cast | `is`, `as` |

`as` on a failed cast yields the diagnosable error `type_cast_failed`; `is`
on a non-object operand is safe and yields `false` — neither panics.

## 6. Lambdas and closures

A lambda is an anonymous function in expression position, using the same
parameter/return syntax as `fun`:

```spore
var f = fun(x: int): int { return x * 2 }
var g = fun(x: int): int = x * 2          // expression body
```

Arrow short form (single-expression body only):

```spore
var dbl: any = (x): int => x * 2        // parenthesized params, typed
var inc: any = x => x + 1                // single untyped param, no parens
var add: any = (a, b) => a + b           // multiple untyped params
var mk: any  = (): int => 42             // zero params
```

Rules:

- Parenthesized form: `(p: T?, ...): R? => expr`; annotations optional;
  function types allowed (`(cb: fun(int): int): int => cb(1)`).
- `p => expr` has no parentheses and no annotation.
- The body is exactly one expression; block bodies require the `fun` form.
- `=>` is a distinct token from `->`, `>=`, `==`.

Closure semantics:

- Captured variables are **shared by reference** with the enclosing scope —
  assignments inside the lambda are visible outside and vice versa.
- `for in` loop variables use **per-iteration binding**: each iteration gets a
  fresh binding, so closures created in different iterations observe their own
  value. C-style `for` variables use a single shared binding.
- Capturing a variable before its declaration is a compile error.
- Assigning a lambda to a function-typed slot is **checked at compile time**:
  parameter count, parameter types, and declared return type must match. A
  component left `any`/unannotated bypasses the check for that component.

## 7. Numeric literal semantics

The language has no numeric suffixes. Integer literals parse as `int64` and
float literals as `float64`; the **declared target type** (a *type hint*)
decides the literal encoding:

| Declaration | Encoding |
|---|---|
| `var x: int = 1` | 32-bit int |
| `var x: long = 1` | 64-bit int |
| `var x: double = 1.0` | 64-bit float |
| `var d = 1.5` (unhinted) | 32-bit float |

Type hints apply on declaration initializers, `return` values (by the
declared return type), lambda/expression bodies, and struct literal field
values — but **not** on plain `=` re-assignment. Prefer explicit `long` /
`double` annotations for values beyond 32-bit ranges.

## 8. Streaming callables (`stream fun`)

- `stream fun name(...): T` declares a streaming callable with a single
  script-visible item type `T`.
- `yield expr` is legal only inside `stream fun`; it emits one intermediate
  item and requires `expr: T`. At the binding layer it lowers to `next`.
- `return expr` emits the terminal item (lowers to `final`) and exhausts the
  stream session. It may be the first externally observed emission.
- After a successful `final`, further `next`/`final` is out-of-contract and
  surfaces structured stream exhaustion.
- `yield` is not allowed inside a `try` block or a `defer` body
  (`yield_disallowed_in_try` / `yield_in_defer`): the catch/defer context
  cannot be preserved across a suspension. `yield` inside a `catch` body is
  allowed — the handler is consumed at catch entry.
- "Skip bad items" loops: move the error-prone segment into a helper whose
  try/catch does not span a suspension, then yield its result:

```spore
fun safeStep(i: int): int {
    try { return risky(i) } catch (e) { return 0 }
}

stream fun pipeline(items: int[]): int {
    for i in items { yield safeStep(i) }
    return 0
}
```

- Message-style streams conventionally use `T = MessageStreamEvent` with
  `kind` distinguishing `start` / `delta` / `end`.

## 9. Modules and host interop

Import forms:

```spore
import add from "math"                      // single symbol
import add as plus from "math"              // single symbol, local alias
import { add, version } from "math"         // named list
import { add as plus, version as v } from "math"  // named list with aliases
export add from "math"                      // re-export
```

- The same syntax covers script modules and **native host modules** — a Go
  `rt.BindFunc("math", "add", ...)` registration is imported as
  `import { add } from "math"`.
- `export struct` and `export type` are compile-time exports: importers see
  the type name and descriptor shape, not a runtime handle.
- Cyclic **compile-time** type imports (mutual `export struct`/`export type`)
  are allowed. Cyclic **runtime** symbol imports are rejected with the
  `cyclic_import` diagnostic.

## 10. Diagnostics index

| Diagnostic | Meaning |
|---|---|
| `missing_struct_field` | struct literal omitted a field without `..` |
| `type_cast_failed` | `as` cast failed |
| `cyclic_import` | runtime symbol import cycle |
| `yield_disallowed_in_try` | `yield` inside a `try` block |
| `yield_in_defer` | `yield` inside a `defer` body |
| `interface_method_missing` | abstract interface method not implemented |
