import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { clearCsrfToken, getCsrfToken } from "./csrf";

beforeEach(() => {
  clearCsrfToken();
  vi.stubGlobal("fetch", vi.fn());
});

afterEach(() => {
  vi.unstubAllGlobals();
  clearCsrfToken();
});

describe("getCsrfToken", () => {
  it("dedupes concurrent calls into a single fetch", async () => {
    const fetchMock = global.fetch as ReturnType<typeof vi.fn>;
    fetchMock.mockResolvedValue(
      new Response(null, { status: 200, headers: { "x-csrf-token": "tok-1" } }),
    );

    const [a, b, c] = await Promise.all([
      getCsrfToken(),
      getCsrfToken(),
      getCsrfToken(),
    ]);

    expect(a).toBe("tok-1");
    expect(b).toBe("tok-1");
    expect(c).toBe("tok-1");
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("re-fetches after clearCsrfToken", async () => {
    const fetchMock = global.fetch as ReturnType<typeof vi.fn>;
    fetchMock
      .mockResolvedValueOnce(
        new Response(null, { status: 200, headers: { "x-csrf-token": "tok-1" } }),
      )
      .mockResolvedValueOnce(
        new Response(null, { status: 200, headers: { "x-csrf-token": "tok-2" } }),
      );

    expect(await getCsrfToken()).toBe("tok-1");
    clearCsrfToken();
    expect(await getCsrfToken()).toBe("tok-2");
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
});
