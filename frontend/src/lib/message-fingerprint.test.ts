import { createHash } from "node:crypto";
import { describe, expect, it } from "vitest";
import type { Message } from "./api";
import { DEFAULT_COMPARE_OPTIONS, messageContent } from "./message-compare";
import {
  type FingerprintOptions,
  fingerprintFlags,
  fingerprintInput,
  messageFingerprint,
  parseFingerprint,
} from "./message-fingerprint";

function msg(over: Partial<Message> = {}): Message {
  return {
    partition: 0,
    offset: 0,
    timestamp_ms: 0,
    key_encoding: "null",
    value_encoding: "text",
    value: "hello",
    ...over,
  };
}

const exact: FingerprintOptions = { ...DEFAULT_COMPARE_OPTIONS, semantic: false };
const semantic: FingerprintOptions = { ...DEFAULT_COMPARE_OPTIONS, semantic: true };

async function fp(m: Message, o: FingerprintOptions): Promise<string> {
  const r = await messageFingerprint(messageContent(m), o);
  if (!r.ok) throw new Error(r.reason);
  return r.fingerprint;
}

describe("message fingerprints", () => {
  it("hashes a stable, length-prefixed serialization", async () => {
    const input = fingerprintInput(messageContent(msg()), exact);
    expect(new TextDecoder().decode(input)).toBe("kfp1\nN\nT5:hello\nH0\n");
    const sha = createHash("sha256").update(input).digest("hex");
    expect(await fp(msg(), exact)).toBe(`kfp1:xh:${sha}`);
  });

  it("includes key and headers", async () => {
    const m = msg({ key_encoding: "text", key: "k", headers: { b: "2", a: "1" } });
    expect(new TextDecoder().decode(fingerprintInput(messageContent(m), exact))).toBe(
      "kfp1\nT1:k\nT5:hello\nH2\nT1:a\nT1:1\nT1:b\nT1:2\n",
    );
  });

  it("matches equivalent JSON only in the semantic variant", async () => {
    const a = msg({ value_encoding: "json", value: '{"a":1,"b":[1,2]}' });
    const b = msg({ value_encoding: "json", value: '{ "b": [1, 2], "a": 1 }' });
    expect(await fp(a, exact)).not.toBe(await fp(b, exact));
    expect(await fp(a, semantic)).toBe(await fp(b, semantic));
  });

  it("leaves kafkito provenance headers out when asked", async () => {
    const a = msg();
    const b = msg({ headers: { "X-Kafkito-Source": "true" } });
    expect(await fp(a, exact)).toBe(await fp(b, exact));
    const strict = { ...exact, ignoreKafkitoHeaders: false };
    expect(await fp(a, strict)).not.toBe(await fp(b, strict));
  });

  it("refuses to fingerprint a preview", async () => {
    const r = await messageFingerprint(
      messageContent(msg({ value_truncated: true, masked: true })),
      exact,
    );
    expect(r).toEqual({ ok: false, reason: "Only a preview of the value is available." });
  });

  it("refuses a semantic fingerprint of JSON above 1 MB but takes the exact one", async () => {
    const value = JSON.stringify({ pad: "x".repeat(1024 * 1024) });
    const content = messageContent(msg({ value_encoding: "json", value }));
    expect(await messageFingerprint(content, semantic)).toEqual({
      ok: false,
      reason: "Too large for a semantic fingerprint (over 1 MB); use the exact view.",
    });
    expect((await messageFingerprint(content, exact)).ok).toBe(true);
  });

  it("does not let field boundaries shift", async () => {
    const a = msg({ key_encoding: "text", key: "ab", value: "c" });
    const b = msg({ key_encoding: "text", key: "a", value: "bc" });
    expect(await fp(a, exact)).not.toBe(await fp(b, exact));
  });
});

describe("fingerprint flags", () => {
  it("encodes the options", () => {
    expect(fingerprintFlags(exact)).toBe("xh");
    expect(fingerprintFlags({ ...semantic, ignoreArrayOrder: true, ignoreChildOrder: true })).toBe(
      "sach",
    );
    // Order options mean nothing for the exact variant.
    expect(
      fingerprintFlags({ ...exact, ignoreArrayOrder: true, ignoreKafkitoHeaders: false }),
    ).toBe("x");
  });

  it("parses what it formats and nothing else", async () => {
    const f = await fp(msg(), { ...semantic, ignoreArrayOrder: true });
    expect(parseFingerprint(` ${f.toUpperCase()} `)).toEqual({
      options: {
        semantic: true,
        ignoreArrayOrder: true,
        ignoreChildOrder: false,
        ignoreKafkitoHeaders: true,
      },
      hex: f.slice(-64),
    });
    const hex = "0".repeat(64);
    for (const bad of [
      `kfp1:xa:${hex}`,
      `kfp1:hs:${hex}`,
      `kfp1:ss:${hex}`,
      `kfp2:x:${hex}`,
      "kfp1:x:abc",
      "sha256:abc",
    ]) {
      expect(parseFingerprint(bad), bad).toBeNull();
    }
  });
});
