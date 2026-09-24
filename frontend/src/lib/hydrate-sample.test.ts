import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  hydrateTruncatedSampleMessages,
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

  it("hydrates a truncated JSON value", async () => {
    const full = JSON.stringify({ order: { id: "A1", price: 9.99 } });
    fetchMessageRawBase64.mockResolvedValue(
      Buffer.from(full, "utf8").toString("base64"),
    );
    const msgs = [
      message({
        partition: 2,
        offset: 55,
        value: '{"order":{"id":"A1"',
        value_truncated: true,
      }),
    ];

    const result = await hydrateTruncatedSampleMessages(
      "my-cluster",
      "my-topic",
      msgs,
    );

    expect(result).toEqual([
      { ...msgs[0], value: full, value_truncated: false },
    ]);
    expect(fetchMessageRawBase64).toHaveBeenCalledWith(
      "my-cluster",
      "my-topic",
      2,
      55,
      undefined,
    );
  });

  it("falls back to the truncated preview when the full value can't be fetched", async () => {
    fetchMessageRawBase64.mockRejectedValue(
      new RawValueTooLargeError("too large"),
    );
    const msgs = [
      message({ value: '{"a":1', value_truncated: true }),
    ];

    const result = await hydrateTruncatedSampleMessages("c", "t", msgs);

    expect(result).toEqual(msgs);
  });

  it("hydrates multiple messages independently and preserves order", async () => {
    fetchMessageRawBase64
      .mockResolvedValueOnce(
        Buffer.from(JSON.stringify({ a: 1 }), "utf8").toString("base64"),
      )
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
    fetchMessageRawBase64.mockResolvedValue(
      Buffer.from("{}", "utf8").toString("base64"),
    );
    const controller = new AbortController();
    const msgs = [message({ value: '{"a":1', value_truncated: true })];

    await hydrateTruncatedSampleMessages("c", "t", msgs, controller.signal);

    expect(fetchMessageRawBase64).toHaveBeenCalledWith(
      "c",
      "t",
      0,
      1,
      controller.signal,
    );
  });
});
