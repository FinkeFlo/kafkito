import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./csrf", () => ({
  getCsrfToken: vi.fn(),
  clearCsrfToken: vi.fn(),
}));

import { apiFetch, SessionExpiredError } from "./api";
import { clearCsrfToken, getCsrfToken } from "./csrf";

const mockedGetCsrfToken = vi.mocked(getCsrfToken);
const mockedClearCsrfToken = vi.mocked(clearCsrfToken);

function makeResponse(
  status: number,
  headers: Record<string, string> = {},
): Response {
  return new Response(null, { status, headers });
}

function getFetchInit(callIndex = 0): RequestInit {
  const fetchMock = global.fetch as unknown as ReturnType<typeof vi.fn>;
  return fetchMock.mock.calls[callIndex][1] as RequestInit;
}

function getFetchHeaders(callIndex = 0): Headers {
  const init = getFetchInit(callIndex);
  return init.headers instanceof Headers
    ? init.headers
    : new Headers(init.headers);
}

beforeEach(() => {
  vi.stubGlobal("fetch", vi.fn());
  mockedGetCsrfToken.mockReset();
  mockedClearCsrfToken.mockReset();
  vi.spyOn(window.location, "assign").mockImplementation(() => {});
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("apiFetch — method handling and CSRF header injection", () => {
  it("defaults to GET when no method is provided and does not inject x-csrf-token", async () => {
    (global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce(
      makeResponse(200),
    );

    await apiFetch("/x");

    const init = getFetchInit();
    const headers = getFetchHeaders();
    expect(init.method).toBe("GET");
    expect(headers.has("x-csrf-token")).toBe(false);
    expect(mockedGetCsrfToken).not.toHaveBeenCalled();
  });

  it.each([["GET"], ["HEAD"], ["get"], ["head"]])(
    "read method %s does not inject x-csrf-token (negative control)",
    async (method) => {
      (global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce(
        makeResponse(200),
      );

      await apiFetch("/x", { method });

      const headers = getFetchHeaders();
      expect(headers.has("x-csrf-token")).toBe(false);
      expect(mockedGetCsrfToken).not.toHaveBeenCalled();
    },
  );

  it.each([["POST"], ["PUT"], ["PATCH"], ["DELETE"]])(
    "write method %s injects x-csrf-token from getCsrfToken()",
    async (method) => {
      const writeToken = "tok-abc";
      mockedGetCsrfToken.mockResolvedValueOnce(writeToken);
      (global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce(
        makeResponse(200),
      );

      await apiFetch("/x", { method });

      const headers = getFetchHeaders();
      expect(headers.get("x-csrf-token")).toBe(writeToken);
      expect(mockedGetCsrfToken).toHaveBeenCalledTimes(1);
    },
  );

  it("normalises lower-case 'post' to upper-case so write-method detection still triggers CSRF injection", async () => {
    const writeToken = "tok-lower";
    mockedGetCsrfToken.mockResolvedValueOnce(writeToken);
    (global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce(
      makeResponse(200),
    );

    await apiFetch("/x", { method: "post" });

    const init = getFetchInit();
    const headers = getFetchHeaders();
    expect(init.method).toBe("POST");
    expect(headers.get("x-csrf-token")).toBe(writeToken);
  });

  it("swallows getCsrfToken() rejection on the write path and proceeds without the header", async () => {
    mockedGetCsrfToken.mockRejectedValueOnce(new Error("csrf endpoint down"));
    (global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce(
      makeResponse(200),
    );

    const res = await apiFetch("/x", { method: "POST" });

    const init = getFetchInit();
    const headers = getFetchHeaders();
    expect(res.status).toBe(200);
    expect(init.method).toBe("POST");
    expect(headers.has("x-csrf-token")).toBe(false);
    expect(global.fetch).toHaveBeenCalledTimes(1);
  });

  it.each([
    ["GET", false],
    ["POST", true],
  ])(
    "always sets credentials: 'include' (method=%s, expectsCsrf=%s)",
    async (method, expectsCsrf) => {
      if (expectsCsrf) mockedGetCsrfToken.mockResolvedValueOnce("tok");
      (global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce(
        makeResponse(200),
      );

      await apiFetch("/x", { method });

      const init = getFetchInit();
      expect(init.credentials).toBe("include");
    },
  );

  it("merges caller-provided headers with the injected CSRF header on writes", async () => {
    const writeToken = "tok-merge";
    mockedGetCsrfToken.mockResolvedValueOnce(writeToken);
    (global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce(
      makeResponse(200),
    );

    await apiFetch("/x", {
      method: "POST",
      headers: { "x-trace": "t1" },
    });

    const headers = getFetchHeaders();
    expect(headers.get("x-trace")).toBe("t1");
    expect(headers.get("x-csrf-token")).toBe(writeToken);
  });
});

describe("apiFetch — 401 auth-loss redirect", () => {
  it("on 401 clears the CSRF cache, navigates to '/', and throws SessionExpiredError", async () => {
    (global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce(
      makeResponse(401),
    );

    await expect(apiFetch("/x")).rejects.toThrow(SessionExpiredError);

    expect(mockedClearCsrfToken).toHaveBeenCalledTimes(1);
    expect(window.location.assign).toHaveBeenCalledTimes(1);
    expect(window.location.assign).toHaveBeenCalledWith("/");
  });

  it("on 200 (negative control) does NOT call window.location.assign and does NOT throw", async () => {
    (global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce(
      makeResponse(200),
    );

    const res = await apiFetch("/x");

    expect(res.status).toBe(200);
    expect(window.location.assign).not.toHaveBeenCalled();
    expect(mockedClearCsrfToken).not.toHaveBeenCalled();
  });
});

describe("apiFetch — 403 CSRF retry semantics", () => {
  it("on 403 with x-csrf-token: Required, clears the cache, retries once with a fresh token, and returns the second response", async () => {
    const firstToken = "tok-stale";
    const secondToken = "tok-fresh";
    mockedGetCsrfToken
      .mockResolvedValueOnce(firstToken)
      .mockResolvedValueOnce(secondToken);

    const fetchMock = global.fetch as ReturnType<typeof vi.fn>;
    fetchMock
      .mockResolvedValueOnce(
        makeResponse(403, { "x-csrf-token": "Required" }),
      )
      .mockResolvedValueOnce(makeResponse(200));

    const requestBody = JSON.stringify({ payload: "p" });
    const res = await apiFetch("/x", {
      method: "POST",
      body: requestBody,
      headers: { "x-trace": "t1" },
    });

    expect(res.status).toBe(200);
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(mockedClearCsrfToken).toHaveBeenCalledTimes(1);

    const firstHeaders = getFetchHeaders(0);
    const secondHeaders = getFetchHeaders(1);
    expect(firstHeaders.get("x-csrf-token")).toBe(firstToken);
    expect(secondHeaders.get("x-csrf-token")).toBe(secondToken);
    expect(secondHeaders.get("x-trace")).toBe("t1");

    const secondInit = getFetchInit(1);
    expect(secondInit.method).toBe("POST");
    expect(secondInit.body).toBe(requestBody);
    expect(secondInit.credentials).toBe("include");
  });

  it("on 403 with x-csrf-token != 'Required' (negative control) does NOT retry and returns the 403", async () => {
    mockedGetCsrfToken.mockResolvedValueOnce("tok");
    const fetchMock = global.fetch as ReturnType<typeof vi.fn>;
    fetchMock.mockResolvedValueOnce(
      makeResponse(403, { "x-csrf-token": "Forbidden" }),
    );

    const res = await apiFetch("/x", { method: "POST" });

    expect(res.status).toBe(403);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(mockedClearCsrfToken).not.toHaveBeenCalled();
  });

  it("on 403 with no x-csrf-token response header (second negative control) does NOT retry", async () => {
    mockedGetCsrfToken.mockResolvedValueOnce("tok");
    const fetchMock = global.fetch as ReturnType<typeof vi.fn>;
    fetchMock.mockResolvedValueOnce(makeResponse(403));

    const res = await apiFetch("/x", { method: "POST" });

    expect(res.status).toBe(403);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(mockedClearCsrfToken).not.toHaveBeenCalled();
  });
});

describe("SessionExpiredError", () => {
  it("is an Error subclass with the documented message", () => {
    const err = new SessionExpiredError();

    expect(err).toBeInstanceOf(Error);
    expect(err).toBeInstanceOf(SessionExpiredError);
    expect(err.message).toBe("session expired");
  });
});
