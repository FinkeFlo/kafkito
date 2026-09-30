import { afterEach, describe, expect, it, vi } from "vitest";
import {
  downloadMessageRaw,
  fetchMessageRawBase64,
  RawValueMaskedError,
  RawValueTooLargeError,
} from "./api";

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("raw value fetchers", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  const masked = { error: "value is masked and cannot be downloaded", code: "value_masked" };

  it("throw RawValueMaskedError with the server's message on 403 value_masked", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(403, masked)),
    );

    const b64 = fetchMessageRawBase64("c", "t", 0, 1).catch((e: unknown) => e);
    const dl = downloadMessageRaw("c", "t", 0, 1).catch((e: unknown) => e);

    for (const err of [await b64, await dl]) {
      expect(err).toBeInstanceOf(RawValueMaskedError);
      expect((err as Error).message).toBe("value is masked and cannot be downloaded");
    }
  });

  it("keep a plain error for other 403 responses", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(403, { error: "forbidden" })),
    );

    const err = await fetchMessageRawBase64("c", "t", 0, 1).catch((e: unknown) => e);

    expect(err).not.toBeInstanceOf(RawValueMaskedError);
    expect((err as Error).message).toBe("HTTP 403: forbidden");
  });

  it("keep RawValueTooLargeError for 413", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse(413, { error: "too big" })),
    );

    const err = await fetchMessageRawBase64("c", "t", 0, 1).catch((e: unknown) => e);

    expect(err).toBeInstanceOf(RawValueTooLargeError);
    expect((err as Error).message).toBe("HTTP 413: too big");
  });

  it("ask for the stored bytes only when decoded is false", async () => {
    const fetchMock = vi.fn(
      async () =>
        new Response(new Uint8Array([0, 0, 0, 0, 7, 2]), {
          status: 200,
          headers: { "Content-Type": "application/octet-stream" },
        }),
    );
    vi.stubGlobal("fetch", fetchMock);

    await fetchMessageRawBase64("c", "t", 0, 1);
    await fetchMessageRawBase64("c", "t", 0, 1, undefined, false);

    const urls = fetchMock.mock.calls.map((args: unknown[]) => {
      const input = args[0];
      return input instanceof Request ? input.url : String(input);
    });
    expect(urls[0]).not.toContain("decoded");
    expect(urls[1]).toMatch(/\/messages\/0\/1\/raw\?decoded=false$/);
  });
});
