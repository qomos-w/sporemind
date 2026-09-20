/**
 * Schema descriptors mirror those declared in `spore/schema/types.go`.
 *
 * These are the canonical vocabulary for describing types and callable
 * signatures across schema, transport, runtime carrier, and script binding.
 * The TypeScript surface uses lowerCamelCase for field names, while values
 * encoded on the wire follow whatever names the originating Go side
 * produced (typically PascalCase). Codegen output uses the schema name
 * verbatim to preserve JSON round-trip parity.
 */

/** Classification of a TypeDesc. */
export type TypeKind =
  | "invalid"
  | "void"
  | "scalar"
  | "array"
  | "map"
  | "struct"
  | "class"
  | "media";

/** Canonical schema type descriptor. */
export interface TypeDesc {
  kind: TypeKind;
  /** Type name (scalar) or struct/class friendly name. */
  name?: string;
  /** Foundational type ID; encoded on the Go side. */
  typeId?: number;
  element?: TypeDesc;
  key?: TypeDesc;
  value?: TypeDesc;
  className?: string;
  classId?: number;
}

/** A named field inside a struct or class. */
export interface FieldDesc {
  name: string;
  type: TypeDesc;
  /** Human-readable documentation, sourced from the `description:` struct tag on the Go side. */
  description?: string;
  optional?: boolean;
  private?: boolean;
}

/** Method declaration on a class shape. */
export interface MethodDesc {
  name: string;
  parameters: ParameterDesc[];
  returns: TypeDesc[];
  isOpen?: boolean;
  isOverride?: boolean;
  private?: boolean;
}

/** A struct or class shape with fields and (for classes) methods. */
export interface ObjectDesc {
  kind: "struct" | "class";
  name: string;
  fields: FieldDesc[];
  parent?: string;
  isOpen?: boolean;
  implements?: string[];
  methods?: MethodDesc[];
}

/** A named parameter in a callable signature. */
export interface ParameterDesc {
  name: string;
  type: TypeDesc;
}

export type CallableMode = "unary" | "streaming";

/** Streaming protocol descriptor — see `spore/schema/types.go`. */
export interface StreamingCallableDesc {
  next?: TypeDesc;
  final?: TypeDesc;
  message?: MessageStreamingDesc;
}

/** Message-style streaming events layered over a streaming callable. */
export interface MessageStreamingDesc {
  start?: TypeDesc;
  delta?: TypeDesc;
  end?: TypeDesc;
}

/** Canonical descriptor for a callable function. */
export interface CallableDesc {
  name: string;
  parameters: ParameterDesc[];
  returns: TypeDesc[];
  hasError: boolean;
  mode: CallableMode;
  streaming?: StreamingCallableDesc;
}

/** Descriptor for an interface declaration. */
export interface InterfaceDesc {
  name: string;
  methods: MethodDesc[];
}
