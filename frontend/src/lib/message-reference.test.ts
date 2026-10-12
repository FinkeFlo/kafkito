import { describe, expect, it } from "vitest";
import { formatMessageRef, parseIndex, parseMessageRef } from "./message-reference";

describe("message references", () => {
  it("round-trips through the text form", () => {
    const ref = { cluster: "prod-eu", topic: "orders.v1", partition: 3, offset: 18842 };
    expect(formatMessageRef(ref)).toBe("prod-eu/orders.v1/3/18842");
    expect(parseMessageRef(formatMessageRef(ref))).toEqual(ref);
  });

  it("allows / and spaces in the cluster name", () => {
    expect(parseMessageRef(" team a/prod/orders/0/7 ")).toEqual({
      cluster: "team a/prod",
      topic: "orders",
      partition: 0,
      offset: 7,
    });
  });

  it("rejects text that is not a reference", () => {
    for (const bad of [
      "",
      "orders/3/18842",
      "prod/orders/x/1",
      "prod/orders/1/-1",
      "prod/orders/1/1.5",
      "prod/or ders/1/1",
      "/orders/1/1",
      "prod/orders/99999999999/1",
    ]) {
      expect(parseMessageRef(bad), bad).toBeNull();
    }
  });
});

describe("parseIndex", () => {
  it("accepts non-negative integers only", () => {
    expect(parseIndex("0")).toBe(0);
    expect(parseIndex("42")).toBe(42);
    expect(parseIndex("")).toBeNull();
    expect(parseIndex("-1")).toBeNull();
    expect(parseIndex("1e3")).toBeNull();
    expect(parseIndex("99999999999999999")).toBeNull();
  });
});
