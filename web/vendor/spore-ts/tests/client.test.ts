import { describe, expect, it } from "vitest";
import type {
  ClientTransport,
  StreamCall,
  StreamSpec,
  UnaryCall,
  UnarySpec,
} from "../src/client.js";

/**
 * client.ts ships only TYPES, not concrete classes — there is nothing to
 * exercise at runtime. The tests in this file act as compile-time fixtures:
 * they would fail to typecheck if the surface drifted (e.g. if `StreamCall`
 * lost `next()` or if `ClientTransport.unary` no longer returned
 * `UnaryCall<Resp>`).
 *
 * Each `expect` is a runtime tautology — the value of these tests is
 * vitest's TypeScript pass over them.
 */

describe("ClientTransport surface", () => {
  it("UnarySpec / StreamSpec carry the expected fields", () => {
    const u: UnarySpec = {
      namespace: "auth",
      name: "lookup",
      reqSchemaId: 1,
      finalSchemaId: 2,
    };
    const s: StreamSpec = {
      namespace: "auth",
      name: "tail",
      reqSchemaId: 3,
      chunkSchemaId: 4,
      finalSchemaId: 5,
    };
    expect(u.namespace).toBe("auth");
    expect(s.chunkSchemaId).toBe(4);
  });

  it("ClientTransport.unary returns a UnaryCall whose final() resolves Resp", async () => {
    interface Req {
      id: string;
    }
    interface Resp {
      ok: boolean;
    }
    const transport: ClientTransport = {
      unary<R, S>(_spec: UnarySpec, _req: R): UnaryCall<S> {
        return {
          final: async () => ({ ok: true }) as unknown as S,
          cancel: () => {},
        };
      },
      stream<R, C, F>(_spec: StreamSpec, _req: R): StreamCall<C, F> {
        return {
          next: async () => undefined,
          final: async () => ({}) as unknown as F,
          cancel: () => {},
        };
      },
    };
    const call = transport.unary<Req, Resp>(
      { namespace: "x", name: "n", reqSchemaId: 1, finalSchemaId: 2 },
      { id: "abc" },
    );
    const resp = await call.final();
    expect(resp.ok).toBe(true);
  });

  it("ClientTransport.stream returns a StreamCall whose next() can drain to undefined", async () => {
    interface Req {
      limit: number;
    }
    interface Chunk {
      seq: number;
    }
    interface Final {
      total: number;
    }
    const chunks: Chunk[] = [{ seq: 1 }, { seq: 2 }];
    const transport: ClientTransport = {
      unary<R, S>(_spec: UnarySpec, _req: R): UnaryCall<S> {
        return { final: async () => ({}) as unknown as S, cancel: () => {} };
      },
      stream<R, C, F>(_spec: StreamSpec, _req: R): StreamCall<C, F> {
        let i = 0;
        return {
          next: async (): Promise<C | undefined> => {
            if (i >= chunks.length) return undefined;
            return chunks[i++] as unknown as C;
          },
          final: async () => ({ total: chunks.length }) as unknown as F,
          cancel: () => {},
        };
      },
    };
    const call = transport.stream<Req, Chunk, Final>(
      { namespace: "x", name: "tail", reqSchemaId: 1, chunkSchemaId: 2, finalSchemaId: 3 },
      { limit: 10 },
    );
    expect((await call.next())?.seq).toBe(1);
    expect((await call.next())?.seq).toBe(2);
    expect(await call.next()).toBeUndefined();
    expect((await call.final()).total).toBe(2);
  });
});
