import { describe, expect, it } from "vitest";
import type { PartitionInfo } from "@/lib/api";
import { clampLimit, clampOffset, offsetBoundsFor } from "./browse-params";

const part = (partition: number, start_offset: number, end_offset: number) =>
  ({ partition, start_offset, end_offset }) as PartitionInfo;

describe("offsetBoundsFor", () => {
  const partitions = [part(0, 10, 100), part(1, 0, 40), part(2, 5, 5)];

  it("spans every partition when all are selected", () => {
    expect(offsetBoundsFor(partitions, -1)).toEqual({ min: 0, max: 99 });
  });

  it("uses the selected partition only", () => {
    expect(offsetBoundsFor(partitions, 1)).toEqual({ min: 0, max: 39 });
  });

  it("never lets max drop below min for an empty partition", () => {
    expect(offsetBoundsFor(partitions, 2)).toEqual({ min: 5, max: 5 });
  });

  it("is null when nothing matches", () => {
    expect(offsetBoundsFor(partitions, 7)).toBeNull();
    expect(offsetBoundsFor([], -1)).toBeNull();
  });
});

describe("clampOffset", () => {
  it("floors and clamps into the bounds", () => {
    const bounds = { min: 10, max: 99 };
    expect(clampOffset("42.9", bounds)).toBe(42);
    expect(clampOffset("3", bounds)).toBe(10);
    expect(clampOffset("1000", bounds)).toBe(99);
  });

  it("maps invalid or negative input to 0 before clamping", () => {
    expect(clampOffset("abc", null)).toBe(0);
    expect(clampOffset("-5", null)).toBe(0);
    expect(clampOffset("abc", { min: 10, max: 99 })).toBe(10);
  });

  it("leaves the value unbounded without bounds", () => {
    expect(clampOffset("123456", null)).toBe(123456);
  });
});

describe("clampLimit", () => {
  it("floors valid input", () => {
    expect(clampLimit("7.8")).toBe(7);
  });

  it("caps at 500", () => {
    expect(clampLimit("9999")).toBe(500);
  });

  it("falls back to 50 for invalid or non-positive input", () => {
    expect(clampLimit("0")).toBe(50);
    expect(clampLimit("-3")).toBe(50);
    expect(clampLimit("x")).toBe(50);
  });
});
