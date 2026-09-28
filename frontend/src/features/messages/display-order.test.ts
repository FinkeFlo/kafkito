import { describe, expect, it } from "vitest";
import { orderForDisplay } from "./display-order";

const m = (partition: number, offset: number, timestamp_ms: number) => ({
  partition,
  offset,
  timestamp_ms,
});

describe("orderForDisplay", () => {
  it("newest sorts by timestamp, then partition, then offset, all descending", () => {
    const input = [m(0, 1, 100), m(1, 5, 300), m(0, 2, 300), m(1, 4, 300), m(2, 0, 200)];
    expect(orderForDisplay(input, "newest")).toEqual([
      m(1, 5, 300),
      m(1, 4, 300),
      m(0, 2, 300),
      m(2, 0, 200),
      m(0, 1, 100),
    ]);
  });

  it("newest does not mutate its input", () => {
    const input = [m(0, 1, 100), m(0, 2, 200)];
    orderForDisplay(input, "newest");
    expect(input).toEqual([m(0, 1, 100), m(0, 2, 200)]);
  });

  it("oldest keeps the fetched order", () => {
    const input = [m(0, 2, 200), m(0, 1, 100), m(1, 0, 300)];
    expect(orderForDisplay(input, "oldest")).toEqual(input);
  });

  it.each(["newest", "oldest"] as const)("%s drops repeated partition+offset pairs", (order) => {
    const out = orderForDisplay([m(0, 1, 100), m(0, 1, 100), m(0, 2, 200)], order);
    expect(out).toHaveLength(2);
    expect(new Set(out.map((x) => `${x.partition}-${x.offset}`)).size).toBe(2);
  });
});
