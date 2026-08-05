import { describe, expect, it } from "vitest";
import { produceEncodingFor, replayBlocker } from "./produce-encoding";
import type { Message } from "./api";

// A binary payload as the consumer returns it: `value` is only the truncated
// hex preview (decodeBytes caps it at 64 bytes), `value_b64` has the real bytes.
const BINARY_PREVIEW = "0xdeadbeef";
const BINARY_B64 = "3q2+7w=="; // base64 of de ad be ef

function message(overrides: Partial<Message> = {}): Message {
  return {
    partition: 0,
    offset: 1,
    timestamp_ms: 0,
    key: "k",
    key_encoding: "text",
    value: "v",
    value_encoding: "text",
    ...overrides,
  };
}

describe("produceEncodingFor", () => {
  it("passes text through verbatim", () => {
    expect(produceEncodingFor("hello", undefined, "text")).toEqual({
      value: "hello",
      encoding: "text",
    });
  });

  it("passes json through verbatim (no re-serialisation)", () => {
    expect(produceEncodingFor('{"a": 1}', undefined, "json")).toEqual({
      value: '{"a": 1}',
      encoding: "text",
    });
  });

  it("maps null to text+'' so the payload stays nil (tombstone is faithful here)", () => {
    expect(produceEncodingFor("", undefined, "null")).toEqual({ value: "", encoding: "text" });
  });

  it("maps empty to the 'empty' encoding, not text+'' which would be a tombstone", () => {
    const out = produceEncodingFor("", undefined, "empty");
    expect(out).toEqual({ value: "", encoding: "empty" });
    expect(out?.encoding).not.toBe("text");
  });

  it("sends binary from value_b64, never the truncated hex preview", () => {
    const out = produceEncodingFor(BINARY_PREVIEW, BINARY_B64, "binary");
    expect(out).toEqual({ value: BINARY_B64, encoding: "base64" });
    expect(out?.value).not.toBe(BINARY_PREVIEW);
    expect(out?.value.startsWith("0x")).toBe(false);
  });

  it("refuses binary without base64 bytes rather than sending the preview", () => {
    expect(produceEncodingFor(BINARY_PREVIEW, undefined, "binary")).toBeNull();
    expect(produceEncodingFor(BINARY_PREVIEW, "", "binary")).toBeNull();
  });

  it("treats a binary field with no bytes and no rendering as zero-length", () => {
    expect(produceEncodingFor("", undefined, "binary")).toEqual({ value: "", encoding: "empty" });
  });

  it.each(["avro", "json_schema", "protobuf"])(
    "refuses %s: the wire-format bytes are gone",
    (encoding) => {
      expect(produceEncodingFor('{"decoded": true}', undefined, encoding)).toBeNull();
    },
  );

  it("falls back to text pass-through for an unknown future encoding", () => {
    expect(produceEncodingFor("raw", undefined, "something-new")).toEqual({
      value: "raw",
      encoding: "text",
    });
  });

  it("tolerates a missing rendering", () => {
    expect(produceEncodingFor(undefined, undefined, "text")).toEqual({
      value: "",
      encoding: "text",
    });
  });
});

describe("replayBlocker", () => {
  it("allows a plain text message", () => {
    expect(replayBlocker(message())).toBeNull();
  });

  it("allows a binary value that still has its base64 bytes", () => {
    expect(
      replayBlocker(
        message({ value: BINARY_PREVIEW, value_b64: BINARY_B64, value_encoding: "binary" }),
      ),
    ).toBeNull();
  });

  it("allows null and empty payloads", () => {
    expect(
      replayBlocker(message({ key: "", key_encoding: "null", value: "", value_encoding: "empty" })),
    ).toBeNull();
  });

  it("blocks a masked message", () => {
    const blocker = replayBlocker(message({ masked: true }));
    expect(blocker).not.toBeNull();
    expect(blocker?.reason).toContain("Masked message");
    expect(blocker?.reason).toContain("redacted");
  });

  it("blocks an avro value instead of silently sending the decoded JSON", () => {
    const blocker = replayBlocker(
      message({
        value: '{"id": 1}',
        value_encoding: "avro",
        value_sr: { format: "avro", schema_id: 7 },
      }),
    );
    expect(blocker?.reason).toContain("Schema-Registry-decoded value");
  });

  it("blocks an SR-decoded key", () => {
    const blocker = replayBlocker(message({ key: '{"id": 1}', key_encoding: "protobuf" }));
    expect(blocker?.reason).toContain("Schema-Registry-decoded key");
  });

  it("blocks a binary value whose bytes are missing", () => {
    const blocker = replayBlocker(message({ value: BINARY_PREVIEW, value_encoding: "binary" }));
    expect(blocker?.reason).toContain("Binary value");
  });

  it("blocks a binary key whose bytes are missing", () => {
    const blocker = replayBlocker(message({ key: BINARY_PREVIEW, key_encoding: "binary" }));
    expect(blocker?.reason).toContain("Binary key");
  });

  it("reports masking before an unrecoverable field", () => {
    const blocker = replayBlocker(
      message({ masked: true, value: '{"id": 1}', value_encoding: "avro" }),
    );
    expect(blocker?.reason).toContain("Masked message");
  });

  it("reports the value before the key when both are unrecoverable", () => {
    const blocker = replayBlocker(
      message({ key_encoding: "avro", value_encoding: "binary", value: BINARY_PREVIEW }),
    );
    expect(blocker?.reason).toContain("value");
    expect(blocker?.reason).not.toContain("key");
  });
});
