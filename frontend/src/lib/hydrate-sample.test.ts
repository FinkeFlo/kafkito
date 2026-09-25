import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  hydrateTruncatedSampleMessages,
  isTooLargeToScan,
  MAX_HYDRATE_VALUE_BYTES,
} from "./hydrate-sample";
import { RawValueTooLargeError, type Message } from "./api";

const fetchMessageRawBase64 = vi.hoisted(() => vi.fn());

vi.mock("./api", async (importActual) => {
  const actual = await importActual<typeof import("./api")>();
  return { ...actual, fetchMessageRawBase64 };
});

function message(overrides: Partial<Message> = {}): Message {
  return {
    partition: 0,
    offset: 1,
    timestamp_ms: 0,
    key_encoding: "text",
    value: "{}",
    // The backend classifies a value cut off mid-structure by its first
    // non-whitespace byte, so truncated JSON keeps value_encoding: "json".
    value_encoding: "json",
    ...overrides,
  };
}

describe("hydrateTruncatedSampleMessages", () => {
  beforeEach(() => {
    fetchMessageRawBase64.mockReset();
  });

  it("leaves non-truncated messages untouched", async () => {
    const msgs = [message({ value_truncated: false })];

    const result = await hydrateTruncatedSampleMessages("c", "t", msgs);

    expect(result).toEqual(msgs);
    expect(fetchMessageRawBase64).not.toHaveBeenCalled();
  });

  it("leaves truncated non-JSON text untouched", async () => {
    const msgs = [
      message({
        value: "just a long log line, not JSON",
        value_encoding: "text",
        value_truncated: true,
      }),
    ];

    const result = await hydrateTruncatedSampleMessages("c", "t", msgs);

    expect(result).toEqual(msgs);
    expect(fetchMessageRawBase64).not.toHaveBeenCalled();
  });

  it("hydrates a truncated XML value, so XPath PathSense sees the full document", async () => {
    const full = "<order><id>A1</id><status>shipped</status></order>";
    fetchMessageRawBase64.mockResolvedValue(Buffer.from(full, "utf-8").toString("base64"));
    const msgs = [
      message({
        value: "<order><id>A1</id><sta",
        value_encoding: "xml",
        value_truncated: true,
      }),
    ];

    const result = await hydrateTruncatedSampleMessages("c", "t", msgs, undefined, "xml");

    expect(result[0].value).toBe(full);
    expect(result[0].value_truncated).toBe(false);
    expect(fetchMessageRawBase64).toHaveBeenCalledTimes(1);
  });

  it("does not fetch values of an encoding the caller's tree cannot parse", async () => {
    const msgs = [
      message({ value: "<order><id>A1</id><sta", value_encoding: "xml", value_truncated: true }),
      message({ offset: 2, value: '{"a":1', value_encoding: "json", value_truncated: true }),
    ];

    // JSONPath mode: the XML sample would be discarded by JSON.parse anyway,
    // so downloading up to MAX_HYDRATE_VALUE_BYTES for it is pure waste.
    fetchMessageRawBase64.mockResolvedValue(Buffer.from('{"a":1}', "utf-8").toString("base64"));

    await hydrateTruncatedSampleMessages("c", "t", msgs, undefined, "json");

    expect(fetchMessageRawBase64).toHaveBeenCalledTimes(1);
    expect(fetchMessageRawBase64).toHaveBeenCalledWith("c", "t", 0, 2, undefined);
  });

  it("hydrates a truncated JSON value", async () => {
    const full = JSON.stringify({ order: { id: "A1", price: 9.99 } });
    fetchMessageRawBase64.mockResolvedValue(Buffer.from(full, "utf8").toString("base64"));
    const msgs = [
      message({
        partition: 2,
        offset: 55,
        value: '{"order":{"id":"A1"',
        value_truncated: true,
      }),
    ];

    const result = await hydrateTruncatedSampleMessages("my-cluster", "my-topic", msgs);

    expect(result).toEqual([{ ...msgs[0], value: full, value_truncated: false }]);
    expect(fetchMessageRawBase64).toHaveBeenCalledWith("my-cluster", "my-topic", 2, 55, undefined);
  });

  it("falls back to the truncated preview when the full value can't be fetched", async () => {
    fetchMessageRawBase64.mockRejectedValue(new RawValueTooLargeError("too large"));
    const msgs = [message({ value: '{"a":1', value_truncated: true })];

    const result = await hydrateTruncatedSampleMessages("c", "t", msgs);

    expect(result).toEqual(msgs);
  });

  it("hydrates multiple messages independently and preserves order", async () => {
    fetchMessageRawBase64
      .mockResolvedValueOnce(Buffer.from(JSON.stringify({ a: 1 }), "utf8").toString("base64"))
      .mockRejectedValueOnce(new Error("boom"));
    const msgs = [
      message({ offset: 1, value: '{"a":1', value_truncated: true }),
      message({
        offset: 2,
        value: '{"b":2',
        value_truncated: true,
      }),
      message({ offset: 3, value: '{"c":3}', value_truncated: false }),
    ];

    const result = await hydrateTruncatedSampleMessages("c", "t", msgs);

    expect(result[0]).toEqual({
      ...msgs[0],
      value: JSON.stringify({ a: 1 }),
      value_truncated: false,
    });
    expect(result[1]).toEqual(msgs[1]);
    expect(result[2]).toEqual(msgs[2]);
  });

  it("skips Schema Registry values, whose raw bytes are not JSON", async () => {
    const msgs = [
      message({
        value: '{"order":{"id":"A1"',
        value_truncated: true,
        value_sr: { format: "json", schema_id: 7 },
      } as Partial<Message>),
    ];

    const result = await hydrateTruncatedSampleMessages("c", "t", msgs);

    expect(result).toEqual(msgs);
    expect(fetchMessageRawBase64).not.toHaveBeenCalled();
  });

  it("skips values above the hydrate size limit instead of downloading them", async () => {
    const msgs = [
      message({
        value: '{"a":1',
        value_truncated: true,
        value_size_bytes: MAX_HYDRATE_VALUE_BYTES + 1,
      }),
    ];

    const result = await hydrateTruncatedSampleMessages("c", "t", msgs);

    expect(result).toEqual(msgs);
    expect(fetchMessageRawBase64).not.toHaveBeenCalled();
  });

  it("still hydrates a value exactly at the size limit", async () => {
    fetchMessageRawBase64.mockResolvedValue(
      Buffer.from(JSON.stringify({ a: 1 }), "utf8").toString("base64"),
    );
    const msgs = [
      message({
        value: '{"a":1',
        value_truncated: true,
        value_size_bytes: MAX_HYDRATE_VALUE_BYTES,
      }),
    ];

    const result = await hydrateTruncatedSampleMessages("c", "t", msgs);

    expect(result[0].value_truncated).toBe(false);
    expect(fetchMessageRawBase64).toHaveBeenCalledTimes(1);
  });

  it("forwards the abort signal to the fetch layer", async () => {
    fetchMessageRawBase64.mockResolvedValue(Buffer.from("{}", "utf8").toString("base64"));
    const controller = new AbortController();
    const msgs = [message({ value: '{"a":1', value_truncated: true })];

    await hydrateTruncatedSampleMessages("c", "t", msgs, controller.signal);

    expect(fetchMessageRawBase64).toHaveBeenCalledWith("c", "t", 0, 1, controller.signal);
  });
});

describe("isTooLargeToScan", () => {
  it("flags a truncated value above the hydration cap", () => {
    // The case the user hit: an 8.4 MiB root array that is valid JSON but
    // only ever reaches the builder as its 64 KB preview.
    expect(
      isTooLargeToScan(
        message({
          value_truncated: true,
          value_size_bytes: MAX_HYDRATE_VALUE_BYTES + 1,
        }),
      ),
    ).toBe(true);
  });

  it("does not flag a value the hydrator can still fetch in full", () => {
    expect(
      isTooLargeToScan(
        message({
          value_truncated: true,
          value_size_bytes: MAX_HYDRATE_VALUE_BYTES,
        }),
      ),
    ).toBe(false);
  });

  it("does not flag an untruncated value, whatever its size", () => {
    // Guards against blaming size for a sample that genuinely isn't JSON.
    expect(
      isTooLargeToScan(
        message({
          value_truncated: false,
          value_size_bytes: MAX_HYDRATE_VALUE_BYTES * 4,
        }),
      ),
    ).toBe(false);
  });

  it("treats a missing size as within the cap", () => {
    expect(isTooLargeToScan(message({ value_truncated: true }))).toBe(false);
  });
});
