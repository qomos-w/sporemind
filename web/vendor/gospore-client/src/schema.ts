/**
 * Lightweight schema descriptors for runtime binary codec usage.
 * Mirrors spore-ts SchemaRegistry vocabulary without adding a dependency.
 */

export type TypeKind =
  | "invalid"
  | "void"
  | "scalar"
  | "array"
  | "map"
  | "struct"
  | "class"
  | "media";

export interface TypeDesc {
  kind: TypeKind;
  name?: string;
  typeId?: number;
  element?: TypeDesc;
  key?: TypeDesc;
  value?: TypeDesc;
  className?: string;
  classId?: number;
}

export interface FieldDesc {
  name: string;
  type: TypeDesc;
  description?: string;
  optional?: boolean;
  private?: boolean;
}

/** A named parameter in a callable signature. */
export interface ParameterDesc {
  name: string;
  type: TypeDesc;
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

export interface SchemaEntry {
  namespace: string;
  schemaId: number;
  name: string;
  visibility?: string;
  type: TypeDesc;
  object?: ObjectDesc;
}

/** Lightweight interface for a binary codec. Matches spore-ts BinaryCodec. */
export interface BinaryCodecLike {
  encode(desc: TypeDesc, value: unknown, objectFor?: (name: string) => ObjectDesc | undefined): Uint8Array;
  decode(desc: TypeDesc, data: Uint8Array, objectFor?: (name: string) => ObjectDesc | undefined): unknown;
}

/** Lightweight interface for a schema registry. Matches spore-ts SchemaRegistry. */
export interface SchemaRegistryLike {
  get(namespace: string, schemaId: number): SchemaEntry | undefined;
  getById(schemaId: number): SchemaEntry | undefined;
  objectFor(namespace: string): (name: string) => ObjectDesc | undefined;
}
