import { describe, expect, it } from "vitest";
import * as spore from "../src/index.js";
import type {
  CallableDesc,
  InterfaceDesc,
  MessageStreamingDesc,
  MethodDesc,
  ObjectDesc,
  ParameterDesc,
  StreamingCallableDesc,
  TypeDesc,
} from "../src/schema.js";

describe("schema descriptors", () => {
  it("preserves class type references", () => {
    const desc: TypeDesc = {
      kind: "class",
      name: "Player",
      className: "Player",
      classId: 7,
      typeId: 42,
    };

    expect(desc).toEqual({
      kind: "class",
      name: "Player",
      className: "Player",
      classId: 7,
      typeId: 42,
    });
  });

  it("preserves object descriptors with inheritance and methods", () => {
    const parameters: ParameterDesc[] = [
      { name: "target", type: { kind: "scalar", name: "string" } },
    ];
    const methods: MethodDesc[] = [
      {
        name: "attack",
        parameters,
        returns: [{ kind: "scalar", name: "int" }],
        isOpen: true,
        isOverride: true,
        private: true,
      },
    ];
    const desc: ObjectDesc = {
      kind: "class",
      name: "Mage",
      parent: "Character",
      isOpen: true,
      implements: ["DamageDealer", "Caster"],
      fields: [
        { name: "Mana", type: { kind: "scalar", name: "int" } },
        { name: "Secret", type: { kind: "scalar", name: "string" }, private: true },
      ],
      methods,
    };

    expect(desc.parent).toBe("Character");
    expect(desc.implements).toEqual(["DamageDealer", "Caster"]);
    expect(desc.methods).toHaveLength(1);
    expect(desc.methods?.[0]).toMatchObject({
      name: "attack",
      isOpen: true,
      isOverride: true,
      private: true,
    });
    expect(desc.fields[1]).toMatchObject({
      name: "Secret",
      private: true,
    });
  });

  it("preserves unary and streaming callable descriptors", () => {
    const unary: CallableDesc = {
      name: "lookupUser",
      parameters: [{ name: "id", type: { kind: "scalar", name: "string" } }],
      returns: [{ kind: "struct", name: "User", className: "User" }],
      hasError: true,
      mode: "unary",
    };

    const message: MessageStreamingDesc = {
      start: { kind: "scalar", name: "string" },
      delta: { kind: "map", key: { kind: "scalar", name: "string" }, value: { kind: "scalar", name: "string" } },
      end: { kind: "void" },
    };
    const streaming: StreamingCallableDesc = {
      next: { kind: "array", element: { kind: "scalar", name: "string" } },
      final: { kind: "scalar", name: "int" },
      message,
    };
    const callable: CallableDesc = {
      name: "watchLogs",
      parameters: [{ name: "service", type: { kind: "scalar", name: "string" } }],
      returns: [{ kind: "void" }],
      hasError: false,
      mode: "streaming",
      streaming,
    };

    expect(unary.mode).toBe("unary");
    expect(unary.returns[0]).toMatchObject({ kind: "struct", className: "User" });
    expect(callable.mode).toBe("streaming");
    expect(callable.streaming?.message?.delta).toMatchObject({ kind: "map" });
    expect(callable.streaming?.final).toMatchObject({ kind: "scalar", name: "int" });
  });

  it("preserves interface descriptors", () => {
    const iface: InterfaceDesc = {
      name: "Greeter",
      methods: [
        {
          name: "greet",
          parameters: [{ name: "name", type: { kind: "scalar", name: "string" } }],
          returns: [{ kind: "scalar", name: "string" }],
        },
      ],
    };

    expect(iface).toEqual({
      name: "Greeter",
      methods: [
        {
          name: "greet",
          parameters: [{ name: "name", type: { kind: "scalar", name: "string" } }],
          returns: [{ kind: "scalar", name: "string" }],
        },
      ],
    });
  });
});

describe("schema barrel exports", () => {
  it("re-exports schema modules from the package root", () => {
    expect(spore).toHaveProperty("SchemaRegistry");
    expect(spore).toHaveProperty("JSONCodec");
  });
});
