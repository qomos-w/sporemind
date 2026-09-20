import { describe, expect, it } from "vitest";
import { CallableRegistry, type CallableEntry } from "../src/callables.js";
import * as spore from "../src/index.js";

describe("CallableRegistry", () => {
  const streamEntry: CallableEntry = {
    namespace: "auth",
    name: "tail_logins",
    visibility: "public",
    mode: "streaming",
    reqSchemaId: 1,
    chunkSchemaId: 2,
    finalSchemaId: 3,
    req: { kind: "struct", name: "TailLoginsReq", className: "TailLoginsReq" },
    chunk: { kind: "struct", name: "LoginEvent", className: "LoginEvent" },
    final: { kind: "struct", name: "TailLoginsFinal", className: "TailLoginsFinal" },
  };

  const unaryEntry: CallableEntry = {
    namespace: "auth",
    name: "lookup_user",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4,
    finalSchemaId: 5,
    req: { kind: "struct", name: "LookupUserReq", className: "LookupUserReq" },
    final: { kind: "struct", name: "LookupUserResp", className: "LookupUserResp" },
  };

  it("registers and retrieves a streaming callable by name", () => {
    const reg = new CallableRegistry();
    reg.register(streamEntry);
    expect(reg.get("auth", "tail_logins")).toBe(streamEntry);
  });

  it("retrieves a callable by (namespace, reqSchemaId) for inbound routing", () => {
    const reg = new CallableRegistry();
    reg.register(streamEntry);
    expect(reg.getByReqSchema("auth", 1)).toBe(streamEntry);
    expect(reg.getByReqSchema("auth", 99)).toBeUndefined();
  });

  it("supports unary entries without chunk metadata", () => {
    const reg = new CallableRegistry();
    reg.register(unaryEntry);
    const got = reg.get("auth", "lookup_user");
    expect(got?.mode).toBe("unary");
    expect(got?.chunk).toBeUndefined();
    expect(got?.chunkSchemaId).toBeUndefined();
  });

  it("rejects duplicate (namespace, name)", () => {
    const reg = new CallableRegistry();
    reg.register(streamEntry);
    expect(() => reg.register({ ...streamEntry, reqSchemaId: 99 })).toThrow(
      /callable name collision/,
    );
  });

  it("rejects duplicate (namespace, reqSchemaId)", () => {
    const reg = new CallableRegistry();
    reg.register(streamEntry);
    expect(() =>
      reg.register({
        ...streamEntry,
        name: "different_name",
      }),
    ).toThrow(/callable reqSchemaId collision/);
  });

  it("namespaces() returns sorted unique namespaces", () => {
    const reg = new CallableRegistry();
    reg.register(streamEntry);
    reg.register({ ...unaryEntry, namespace: "billing", reqSchemaId: 10, finalSchemaId: 11 });
    reg.register({ ...streamEntry, namespace: "alpha", reqSchemaId: 20, chunkSchemaId: 21, finalSchemaId: 22 });
    expect(reg.namespaces()).toEqual(["alpha", "auth", "billing"]);
  });

  it("list() snapshots (namespace, name) pairs of registered callables", () => {
    const reg = new CallableRegistry();
    reg.register(streamEntry);
    reg.register(unaryEntry);
    const seen = reg.list();
    expect(seen).toHaveLength(2);
    expect(seen).toContainEqual({ namespace: "auth", name: "tail_logins" });
    expect(seen).toContainEqual({ namespace: "auth", name: "lookup_user" });
  });
});

describe("callables barrel export", () => {
  it("re-exports CallableRegistry from the package root", () => {
    expect(spore).toHaveProperty("CallableRegistry");
  });
});
