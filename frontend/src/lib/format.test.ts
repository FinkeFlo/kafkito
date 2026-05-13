import { describe, expect, it } from "vitest";
import {
  formatBytes,
  formatCount,
  formatDuration,
  formatNumber,
  formatRate,
} from "./format";

const oneSecondMs = 1_000;
const oneMinuteMs = 60_000;
const oneHourMs = 3_600_000;
const oneDayMs = 86_400_000;

const oneKiB = 1_024;
const oneMiB = 1_024 * 1_024;
const oneGiB = 1_024 * 1_024 * 1_024;

const oneThousand = 1_000;
const oneMillion = 1_000_000;
const oneBillion = 1_000_000_000;

describe("formatDuration", () => {
  it.each<[unknown, string]>([
    [null, "—"],
    [undefined, "—"],
    [NaN, "—"],
  ])("returns — for nullish / non-finite (%p)", (input, expected) => {
    expect(formatDuration(input as number | null | undefined)).toBe(expected);
  });

  it.each<[number, string]>([
    [-1, "∞"],
    [-1_000, "∞"],
  ])("returns ∞ for negative %i ms (Kafka 'infinite retention' convention)", (input, expected) => {
    expect(formatDuration(input)).toBe(expected);
  });

  it.each<[number, string]>([
    [0, "0"],
    [1, "1 ms"],
    [250, "250 ms"],
    [999, "999 ms"],
  ])("renders sub-second duration %i ms", (input, expected) => {
    expect(formatDuration(input)).toBe(expected);
  });

  it.each<[number, string]>([
    [1.5 * oneSecondMs, "2 s"],
    [oneMinuteMs, "1 m"],
    [oneHourMs, "1.0 h"],
    [oneDayMs, "1.0 d"],
    [7 * oneDayMs, "7.0 d"],
    [60 * oneDayMs, "2 mo"],
    [365 * oneDayMs, "1.0 y"],
  ])("scales %i ms through s, m, h, d, mo, y", (input, expected) => {
    expect(formatDuration(input)).toBe(expected);
  });
});

describe("formatRate", () => {
  it.each<[unknown, string]>([
    [null, "—"],
    [undefined, "—"],
    [-1, "—"],
    [NaN, "—"],
  ])("handles nullish / negative / non-finite (%p)", (input, expected) => {
    expect(formatRate(input as number | null | undefined)).toBe(expected);
  });

  it.each<[number, string]>([
    [0, "0/s"],
    [0.01, "0/s"],
  ])("shows 0/s for tiny value %f", (input, expected) => {
    expect(formatRate(input)).toBe(expected);
  });

  it.each<[number, string]>([
    [0.5, "0.50/s"],
    [3.4, "3.4/s"],
    [42, "42/s"],
    [999, "999/s"],
    [1.5 * oneThousand, "1.5k/s"],
    [2.5 * oneMillion, "2.50M/s"],
  ])("picks the right unit and precision for %f /s", (input, expected) => {
    expect(formatRate(input)).toBe(expected);
  });
});

describe("formatBytes", () => {
  it.each<[unknown, string]>([
    [null, "—"],
    [undefined, "—"],
  ])("handles nullish (%p)", (input, expected) => {
    expect(formatBytes(input as number | null | undefined)).toBe(expected);
  });

  it.each<[number, string]>([
    [0, "0 B"],
    [512, "512 B"],
    [oneKiB, "1.00 KiB"],
    [oneMiB, "1.00 MiB"],
    [oneGiB, "1.00 GiB"],
  ])("scales IEC unit boundary %i B", (input, expected) => {
    expect(formatBytes(input)).toBe(expected);
  });
});

describe("formatCount", () => {
  it.each<[unknown, string]>([
    [null, "—"],
    [undefined, "—"],
  ])("handles nullish (%p)", (input, expected) => {
    expect(formatCount(input as number | null | undefined)).toBe(expected);
  });

  it.each<[number, string]>([
    [42, "42"],
    [1.5 * oneThousand, "1.5K"],
    [1_965_590, "1.97M"],
    [2.5 * oneMillion, "2.5M"],
    [3.2 * oneBillion, "3.2B"],
  ])("scales k/M/B threshold %i (en-US, Intl compact short)", (input, expected) => {
    expect(formatCount(input)).toBe(expected);
  });

  it.each<[number, string]>([
    [42, "42"],
    [1_500, "1500"],
    [1_965_590, "1,97\u00a0Mio."],
    [2_500_000, "2,5\u00a0Mio."],
    [3_200_000_000, "3,2\u00a0Mrd."],
  ])("uses German decimal separators and CLDR compact suffixes %i (de-DE)", (input, expected) => {
    expect(formatCount(input, "de-DE")).toBe(expected);
  });
});

describe("locale-aware separators (regression: real de-DE behavior)", () => {
  it("groups thousands with dots in de-DE (formatNumber)", () => {
    expect(formatNumber(1_965_590, "de-DE")).toBe("1.965.590");
  });

  it("groups thousands with commas in en-US (formatNumber)", () => {
    expect(formatNumber(1_965_590, "en-US")).toBe("1,965,590");
  });

  it("uses comma decimal separator in de-DE (formatRate)", () => {
    expect(formatRate(3.4, "de-DE")).toBe("3,4/s");
  });

  it("uses comma decimal separator in de-DE (formatBytes)", () => {
    expect(formatBytes(1_536, "de-DE")).toBe("1,50 KiB");
  });

  it("uses comma decimal separator in de-DE (formatDuration)", () => {
    expect(formatDuration(3_600_000, "de-DE")).toBe("1,0 h");
  });
});
