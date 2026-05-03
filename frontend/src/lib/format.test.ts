import { describe, expect, it } from "vitest";
import { formatBytes, formatCount, formatDuration, formatRate } from "./format";

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
    [1.5 * oneThousand, "1.5k"],
    [2.5 * oneMillion, "2.50M"],
    [3.2 * oneBillion, "3.20B"],
  ])("scales k/M/B threshold %i", (input, expected) => {
    expect(formatCount(input)).toBe(expected);
  });
});
