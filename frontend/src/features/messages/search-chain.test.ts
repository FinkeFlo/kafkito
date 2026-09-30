import { beforeEach, describe, expect, it, vi } from "vitest";
import type { SearchRequest, SearchResponse, SearchStats } from "@/lib/api";
import { runSearchChain, type SearchResult } from "./search-chain";

const searchMessages = vi.hoisted(() => vi.fn());

vi.mock("@/lib/api", async (importActual) => {
  const actual = await importActual<typeof import("@/lib/api")>();
  return { ...actual, searchMessages };
});

const baseReq: SearchRequest = { partition: -1, limit: 10, mode: "contains", value: "x" };

function stats(over: Partial<SearchStats>): SearchStats {
  return {
    scanned: 0,
    matched: 0,
    budget_exhausted: false,
    timed_out: false,
    more_available: false,
    direction: "newest_first",
    parse_errors: 0,
    ...over,
  };
}

function page(offsets: number[], over: Partial<SearchStats>): SearchResponse {
  return {
    cluster: "c",
    topic: "t",
    messages: offsets.map((offset) => ({
      partition: 0,
      offset,
      timestamp_ms: offset,
      key_encoding: "null",
      value_encoding: "text",
      value: `v${offset}`,
    })),
    search: stats(over),
  };
}

function run(over: Partial<Parameters<typeof runSearchChain>[0]> = {}) {
  const progress: SearchResult[] = [];
  const done = runSearchChain({
    cluster: "c",
    topic: "t",
    baseReq,
    prior: null,
    budget: 100,
    stopOnLimit: true,
    limit: 10,
    shouldStop: () => false,
    onProgress: (r) => progress.push(r),
    ...over,
  });
  return { done, progress };
}

describe("runSearchChain", () => {
  beforeEach(() => {
    searchMessages.mockReset();
  });

  it("stops as complete when the range is exhausted", async () => {
    searchMessages.mockResolvedValueOnce(page([1], { scanned: 40, matched: 1 }));
    const { done, progress } = run();
    await expect(done).resolves.toBe("complete");
    expect(searchMessages).toHaveBeenCalledWith("c", "t", {
      ...baseReq,
      budget: 100,
      cursors: undefined,
    });
    expect(progress).toHaveLength(1);
    expect(progress[0].stats.scanned).toBe(40);
  });

  it("chains with the remaining budget and the previous cursors, accumulating stats", async () => {
    searchMessages
      .mockResolvedValueOnce(
        page([1], { scanned: 60, matched: 1, more_available: true, next_cursors: { "0": 7 } }),
      )
      .mockResolvedValueOnce(
        page([2], { scanned: 40, matched: 1, more_available: true, next_cursors: { "0": 3 } }),
      );
    const { done, progress } = run();
    await expect(done).resolves.toBe("budget");
    expect(searchMessages).toHaveBeenCalledTimes(2);
    expect(searchMessages.mock.calls[1][2]).toEqual({
      ...baseReq,
      budget: 40,
      cursors: { "0": 7 },
    });
    const last = progress[1];
    expect(last.messages.map((m) => m.offset)).toEqual([1, 2]);
    expect(last.stats).toMatchObject({ scanned: 100, matched: 2, next_cursors: { "0": 3 } });
    expect(last.req.budget).toBe(40);
  });

  it("stops at the limit when stop-on-limit is set", async () => {
    searchMessages.mockResolvedValue(
      page([1, 2, 3], { scanned: 5, matched: 3, more_available: true }),
    );
    const { done } = run({ limit: 5, budget: 1000 });
    await expect(done).resolves.toBe("limit");
    expect(searchMessages).toHaveBeenCalledTimes(2);
  });

  it("ignores the limit without stop-on-limit", async () => {
    searchMessages
      .mockResolvedValueOnce(page([1, 2, 3], { scanned: 5, matched: 3, more_available: true }))
      .mockResolvedValueOnce(page([4, 5, 6], { scanned: 5, matched: 3 }));
    const { done } = run({ limit: 2, stopOnLimit: false, budget: 1000 });
    await expect(done).resolves.toBe("complete");
    expect(searchMessages).toHaveBeenCalledTimes(2);
  });

  it("stops between calls when asked to", async () => {
    searchMessages.mockResolvedValue(page([], { scanned: 5, more_available: true }));
    let stop = false;
    const { done } = run({
      budget: 1000,
      shouldStop: () => stop,
      onProgress: () => {
        stop = true;
      },
    });
    await expect(done).resolves.toBe("stopped");
    expect(searchMessages).toHaveBeenCalledTimes(1);
  });

  it("stops as timed out when a call scanned nothing but reports more", async () => {
    searchMessages.mockResolvedValue(page([], { scanned: 0, more_available: true }));
    const { done } = run({ budget: 0 });
    await expect(done).resolves.toBe("timeout");
    expect(searchMessages).toHaveBeenCalledTimes(1);
  });

  it("stops as timed out when a call did not move any cursor", async () => {
    const range = { "0": { start: 0, end: 50 }, "1": { start: 0, end: 20 } };
    searchMessages.mockResolvedValue(
      page([], {
        scanned: 7,
        timed_out: true,
        more_available: true,
        resolved_range: range,
        next_cursors: { "0": 50, "1": 20 },
      }),
    );
    const { done } = run({ budget: 0 });
    await expect(done).resolves.toBe("timeout");
    expect(searchMessages).toHaveBeenCalledTimes(1);
  });

  it("checks progress against the range start when oldest first", async () => {
    searchMessages.mockResolvedValue(
      page([], {
        scanned: 7,
        timed_out: true,
        more_available: true,
        direction: "oldest_first",
        resolved_range: { "0": { start: 10, end: 50 } },
        next_cursors: { "0": 10 },
      }),
    );
    const { done } = run({ budget: 0 });
    await expect(done).resolves.toBe("timeout");
    expect(searchMessages).toHaveBeenCalledTimes(1);
  });

  it("keeps chaining after a timed-out call that moved a cursor", async () => {
    searchMessages
      .mockResolvedValueOnce(
        page([], {
          scanned: 7,
          timed_out: true,
          more_available: true,
          resolved_range: { "0": { start: 0, end: 50 }, "1": { start: 0, end: 20 } },
          next_cursors: { "0": 40, "1": 20 },
        }),
      )
      .mockResolvedValueOnce(page([], { scanned: 5 }));
    const { done } = run({ budget: 0 });
    await expect(done).resolves.toBe("complete");
    expect(searchMessages).toHaveBeenCalledTimes(2);
    expect(searchMessages.mock.calls[1][2].cursors).toEqual({ "0": 40, "1": 20 });
  });

  it("uses the per-call cap when unlimited", async () => {
    searchMessages
      .mockResolvedValueOnce(page([], { scanned: 5, more_available: true }))
      .mockResolvedValueOnce(page([], { scanned: 5 }));
    const { done } = run({ budget: 0 });
    await expect(done).resolves.toBe("complete");
    expect(searchMessages.mock.calls.map((c) => c[2].budget)).toEqual([1_000_000, 1_000_000]);
  });

  it("continues from a prior result with a fresh budget", async () => {
    searchMessages.mockResolvedValueOnce(page([9], { scanned: 30, matched: 1 }));
    const prior: SearchResult = {
      messages: page([8], {}).messages ?? [],
      stats: stats({ scanned: 100, matched: 1, more_available: true, next_cursors: { "0": 50 } }),
      req: baseReq,
    };
    const { done, progress } = run({ prior });
    await expect(done).resolves.toBe("complete");
    expect(searchMessages.mock.calls[0][2]).toMatchObject({ budget: 100, cursors: { "0": 50 } });
    expect(progress[0].messages.map((m) => m.offset)).toEqual([8, 9]);
    expect(progress[0].stats).toMatchObject({ scanned: 130, matched: 2 });
  });

  it("rejects when a call fails", async () => {
    searchMessages.mockRejectedValueOnce(new Error("boom"));
    const { done, progress } = run();
    await expect(done).rejects.toThrow("boom");
    expect(progress).toHaveLength(0);
  });
});
