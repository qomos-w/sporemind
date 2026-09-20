import { describe, it, expect } from "vitest";
import {
  PayloadEncoding,
  PayloadCompression,
  FrameType,
  makeFlags,
  getEncoding,
  getCompression,
  marshalWireFrame,
  unmarshalWireFrame,
  WireFrame,
  FrameError,
} from "./binary_frame";

describe("binary_frame", () => {
  it("round-trips a minimal frame", () => {
    const src: WireFrame = {
      flags: makeFlags(PayloadEncoding.JSON, PayloadCompression.None),
      type: FrameType.Ping,
      corId: 1n,
      seq: 0,
      transId: 0n,
      callId: "",
      subId: "",
      target: "",
      from: "",
      errorMsg: "",
      payload: new Uint8Array(),
      headers: new Map(),
    };
    const wire = marshalWireFrame(src);
    const got = unmarshalWireFrame(wire);
    expect(got.type).toBe(src.type);
    expect(got.corId).toBe(src.corId);
  });

  it("round-trips a full frame", () => {
    const src: WireFrame = {
      flags: makeFlags(PayloadEncoding.Binary, PayloadCompression.Gzip),
      type: FrameType.Invoke,
      corId: 42n,
      seq: 7,
      transId: 123n,
      callId: "filesystem.list_json",
      subId: "sub-123",
      target: "actor-target",
      from: "actor-0001",
      errorMsg: "",
      payload: new Uint8Array([0x54, 0x42, 0x43, 0x01, 0x02]),
      headers: new Map([
        ["trace", "x"],
        ["auth", "token"],
      ]),
    };
    const wire = marshalWireFrame(src);
    const got = unmarshalWireFrame(wire);
    expect(got.flags).toBe(src.flags);
    expect(got.type).toBe(src.type);
    expect(got.corId).toBe(src.corId);
    expect(got.seq).toBe(src.seq);
    expect(got.transId).toBe(src.transId);
    expect(got.callId).toBe(src.callId);
    expect(got.subId).toBe(src.subId);
    expect(got.target).toBe(src.target);
    expect(got.from).toBe(src.from);
    expect(got.payload).toEqual(src.payload);
    expect(got.headers.get("trace")).toBe("x");
    expect(got.headers.get("auth")).toBe("token");
  });

  it("round-trips empty fields", () => {
    const src: WireFrame = {
      flags: makeFlags(PayloadEncoding.JSON, PayloadCompression.None),
      type: FrameType.Reply,
      corId: 99n,
      seq: 0,
      transId: 0n,
      callId: "",
      subId: "",
      target: "",
      from: "",
      errorMsg: "",
      payload: new Uint8Array(),
      headers: new Map(),
    };
    const wire = marshalWireFrame(src);
    const got = unmarshalWireFrame(wire);
    expect(got.headers.size).toBe(0);
    expect(got.payload.length).toBe(0);
  });

  it("rejects invalid magic", () => {
    const data = new Uint8Array([0, 0, 0, 0, 1, 0, 1]);
    expect(() => unmarshalWireFrame(data)).toThrow(FrameError);
  });

  it("rejects unsupported version", () => {
    const data = new Uint8Array([0x47, 0x53, 0x46, 0x01, 0x99, 0x00, 0x01]);
    expect(() => unmarshalWireFrame(data)).toThrow(FrameError);
  });

  it("rejects truncated data", () => {
    expect(() => unmarshalWireFrame(new Uint8Array())).toThrow(FrameError);
    expect(() => unmarshalWireFrame(new Uint8Array([0x01, 0x02, 0x03]))).toThrow(FrameError);
  });

  it("produces deterministic bytes for headers", () => {
    const a: WireFrame = {
      flags: makeFlags(PayloadEncoding.JSON, PayloadCompression.None),
      type: FrameType.Invoke,
      corId: 1n,
      seq: 0,
      transId: 0n,
      callId: "",
      subId: "",
      target: "",
      from: "",
      errorMsg: "",
      payload: new Uint8Array(),
      headers: new Map([
        ["a", "1"],
        ["b", "2"],
        ["c", "3"],
      ]),
    };
    const b: WireFrame = { ...a, headers: new Map([["c", "3"], ["a", "1"], ["b", "2"]]) };
    const wa = marshalWireFrame(a);
    const wb = marshalWireFrame(b);
    expect(wa).toEqual(wb);
  });

  it("round-trips all flags combinations", () => {
    const cases: [PayloadEncoding, PayloadCompression][] = [
      [PayloadEncoding.JSON, PayloadCompression.None],
      [PayloadEncoding.Binary, PayloadCompression.None],
      [PayloadEncoding.JSON, PayloadCompression.Gzip],
      [PayloadEncoding.Binary, PayloadCompression.Gzip],
    ];
    for (const [enc, comp] of cases) {
      const flags = makeFlags(enc, comp);
      expect(getEncoding(flags)).toBe(enc);
      expect(getCompression(flags)).toBe(comp);
    }
  });

  it("round-trips large payload", () => {
    const src: WireFrame = {
      flags: makeFlags(PayloadEncoding.Binary, PayloadCompression.None),
      type: FrameType.Chunk,
      corId: 12345n,
      seq: 999,
      transId: 456n,
      callId: "stream.chunks",
      subId: "sub-abc",
      target: "",
      from: "",
      errorMsg: "",
      payload: new Uint8Array(1024).fill(0xab),
      headers: new Map(),
    };
    const wire = marshalWireFrame(src);
    const got = unmarshalWireFrame(wire);
    expect(got.payload.length).toBe(1024);
    expect(got.payload[0]).toBe(0xab);
    expect(got.payload[1023]).toBe(0xab);
  });

  it("round-trips error frame", () => {
    const src: WireFrame = {
      flags: makeFlags(PayloadEncoding.JSON, PayloadCompression.None),
      type: FrameType.Error,
      corId: 7n,
      seq: 0,
      transId: 0n,
      callId: "",
      subId: "sub-err",
      target: "",
      from: "",
      errorMsg: "something went wrong",
      payload: new Uint8Array(),
      headers: new Map(),
    };
    const wire = marshalWireFrame(src);
    const got = unmarshalWireFrame(wire);
    expect(got.type).toBe(FrameType.Error);
    expect(got.errorMsg).toBe("something went wrong");
    expect(got.subId).toBe("sub-err");
  });
});
