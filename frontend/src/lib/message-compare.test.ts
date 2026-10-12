import { describe, expect, it } from "vitest";
import type { Message } from "./api";
import {
  compareMessages,
  DEFAULT_COMPARE_OPTIONS,
  hexDump,
  messageContent,
  needsFullValue,
  worstVerdict,
} from "./message-compare";

function msg(over: Partial<Message> = {}): Message {
  return {
    partition: 0,
    offset: 0,
    timestamp_ms: 1_700_000_000_000,
    key_encoding: "text",
    key: "k1",
    value_encoding: "json",
    value: '{"id":1,"tags":["a","b"]}',
    ...over,
  };
}

const opts = DEFAULT_COMPARE_OPTIONS;
const compare = (a: Message, b: Message, o = opts) =>
  compareMessages(messageContent(a), messageContent(b), o);

describe("compareMessages", () => {
  it("calls byte-equal records identical", () => {
    const r = compare(msg(), msg({ partition: 4, offset: 99, timestamp_ms: 1 }));
    expect(r.verdict).toBe("identical");
    expect(r.key.verdict).toBe("identical");
    expect(r.value.verdict).toBe("identical");
    expect(r.headers.verdict).toBe("identical");
  });

  it("calls JSON with reordered keys equivalent and says why", () => {
    const r = compare(msg(), msg({ value: '{ "tags": ["a", "b"], "id": 1.0 }' }));
    expect(r.value.verdict).toBe("equivalent");
    expect(r.verdict).toBe("equivalent");
    expect(r.value.notes.join(" ")).toMatch(/key order/);
    expect(r.value.semantic?.a).toBe(r.value.semantic?.b);
    expect(r.value.exact.a).not.toBe(r.value.exact.b);
  });

  it("needs the array option for reordered arrays", () => {
    const b = msg({ value: '{"id":1,"tags":["b","a"]}' });
    expect(compare(msg(), b).value.verdict).toBe("different");
    expect(compare(msg(), b).value.notes.join(" ")).toMatch(
      /Only the order of array elements differs/,
    );
    const r = compare(msg(), b, { ...opts, ignoreArrayOrder: true });
    expect(r.value.verdict).toBe("equivalent");
    expect(r.value.notes.join(" ")).toMatch(/array order/);
  });

  it("compares XML semantically, with the element order option", () => {
    const a = msg({ value_encoding: "xml", value: '<o a="1" b="2"><i>1</i><i>2</i></o>' });
    const b = msg({ value_encoding: "xml", value: '<o b="2" a="1">\n  <i>2</i><i>1</i>\n</o>' });
    expect(compare(a, b).value.verdict).toBe("different");
    expect(compare(a, b).value.notes.join(" ")).toMatch(/Only the order of elements differs/);
    expect(compare(a, b, { ...opts, ignoreChildOrder: true }).value.verdict).toBe("equivalent");
  });

  it("compares Schema Registry values on their decoded JSON", () => {
    const a = msg({ value_encoding: "avro", value_sr: { format: "avro", schema_id: 1 } });
    const b = msg({ value_encoding: "avro", value_sr: { format: "avro", schema_id: 7 } });
    expect(compare(a, b).value.verdict).toBe("identical");
  });

  it("finds different text values and keys", () => {
    const r = compare(
      msg({ value_encoding: "text", value: "a" }),
      msg({ key: "k2", value_encoding: "text", value: "b" }),
    );
    expect(r.key.verdict).toBe("different");
    expect(r.value.verdict).toBe("different");
    expect(r.verdict).toBe("different");
  });

  it("tells a null key from an empty one", () => {
    const r = compare(
      msg({ key_encoding: "null", key: undefined }),
      msg({ key_encoding: "empty", key: undefined }),
    );
    expect(r.key.verdict).toBe("different");
    expect(r.key.exact).toEqual({ a: "(null)", b: "(empty)" });
  });

  it("compares binary values byte for byte and diffs their hex dumps", () => {
    const a = msg({ value_encoding: "binary", value: "0x0001", value_b64: btoa("\x00\x01") });
    const b = msg({ value_encoding: "binary", value: "0x0002", value_b64: btoa("\x00\x02") });
    const r = compare(a, b);
    expect(r.value.verdict).toBe("different");
    expect(r.value.format).toBe("binary");
    expect(r.value.exact.a).toContain("00 01");
    expect(compare(a, a).value.verdict).toBe("identical");
  });

  it("notes JSON that does not parse and compares it exactly", () => {
    const r = compare(msg(), msg({ value: '{"id":1,' }));
    expect(r.value.verdict).toBe("different");
    expect(r.value.semantic).toBeNull();
    expect(r.value.notes.join(" ")).toMatch(/Side B's value does not parse as JSON/);
  });

  it("does not call a difference real when only one side is masked", () => {
    const r = compare(msg({ masked: true, value: '{"id":1,"tags":"***"}' }), msg());
    expect(r.value.verdict).toBe("not_comparable");
    expect(r.value.notes.join(" ")).toMatch(/Only side A's value is masked/);
  });

  it("does not vouch for values only their previews were compared of", () => {
    const preview = msg({ value_truncated: true, masked: true, value: '{"id":1' });
    expect(compare(preview, preview).value.verdict).toBe("not_comparable");
    // Without the full value, a preview that is a prefix of the other side
    // proves nothing; one that is not proves a difference.
    const unfetched = msg({ value_truncated: true, value: '{"id":1' });
    expect(compare(unfetched, msg()).value.verdict).toBe("not_comparable");
    expect(compare(unfetched, msg({ value: "[]" })).value.verdict).toBe("different");
  });

  it("uses the full value when it was fetched", () => {
    const truncated = msg({ value_truncated: true, value: '{"id":1,"ta', value_size_bytes: 25 });
    const full = btoa('{"id":1,"tags":["a","b"]}');
    const r = compareMessages(messageContent(truncated, full), messageContent(msg()), opts);
    expect(r.value.verdict).toBe("identical");
  });
});

describe("large values", () => {
  const big = (total: number) =>
    JSON.stringify({ items: Array.from({ length: 60_000 }, (_, i) => ({ id: i, n: "x" })), total });

  it("compares JSON above 1 MB exactly only, and says so", () => {
    const a = msg({ value: big(1) });
    expect(a.value?.length).toBeGreaterThan(1024 * 1024);
    const r = compare(a, msg({ value: big(2) }));
    expect(r.value.semantic).toBeNull();
    expect(r.value.verdict).toBe("different");
    expect(r.value.notes.join(" ")).toMatch(/larger than 1 MB; compared exactly/);
    expect(compare(a, msg({ value: big(1) })).value.verdict).toBe("identical");
  });
});

describe("header comparison", () => {
  it("lists headers only on one side and different values", () => {
    const r = compare(
      msg({ headers: { a: "1", b: "2", c: "3" } }),
      msg({ headers: { b: "2", c: "x", d: "4" } }),
    );
    expect(r.headers.rows.map((row) => [row.key, row.status])).toEqual([
      ["a", "only_a"],
      ["b", "same"],
      ["c", "different"],
      ["d", "only_b"],
    ]);
    expect(r.headers.verdict).toBe("different");
  });

  it("ignores kafkito provenance headers by default", () => {
    const r = compare(
      msg({ headers: { id: "1" } }),
      msg({ headers: { id: "1", "X-Kafkito-Source": "true", "x-kafkito-user": "alice" } }),
    );
    expect(r.headers.verdict).toBe("identical");
    expect(r.headers.notes.join(" ")).toMatch(/2 kafkito provenance headers/);
    const strict = compare(
      msg({ headers: { id: "1" } }),
      msg({ headers: { id: "1", "X-Kafkito-Source": "true" } }),
      { ...opts, ignoreKafkitoHeaders: false },
    );
    expect(strict.headers.verdict).toBe("different");
  });

  it("compares non-UTF-8 header values on their bytes", () => {
    const r = compare(
      msg({ headers: { h: "0xff" }, headers_b64: { h: btoa("\xff") } }),
      msg({ headers: { h: "0xff" }, headers_b64: { h: btoa("\xfe") } }),
    );
    expect(r.headers.rows[0].status).toBe("different");
  });

  it("does not count a header difference caused by masking on one side", () => {
    const r = compare(
      msg({ headers: { auth: "***" }, masked_headers: ["auth"] }),
      msg({ headers: { auth: "secret" } }),
    );
    expect(r.headers.verdict).toBe("not_comparable");
  });
});

describe("helpers", () => {
  it("ranks verdicts from identical to different", () => {
    expect(worstVerdict("identical", "equivalent")).toBe("equivalent");
    expect(worstVerdict("equivalent", "not_comparable")).toBe("not_comparable");
    expect(worstVerdict("not_comparable", "different", "identical")).toBe("different");
  });

  it("fetches the full value only for unmasked truncated values within the limit", () => {
    expect(needsFullValue(msg())).toBe(false);
    expect(needsFullValue(msg({ value_truncated: true, value_size_bytes: 100_000 }))).toBe(true);
    expect(needsFullValue(msg({ value_truncated: true, masked: true }))).toBe(false);
    expect(needsFullValue(msg({ value_truncated: true, value_size_bytes: 5 * 1024 * 1024 }))).toBe(
      false,
    );
  });

  it("dumps bytes as offset, hex and printable characters", () => {
    expect(hexDump(btoa("AB\x00"))).toBe(`00000000  41 42 00 ${"   ".repeat(13)} |AB.|`);
  });
});
