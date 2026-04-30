import { describe, expect, it } from "vitest";
import { fuzzyFilter } from "./fuzzy";

interface Item {
  name: string;
}

const topics: Item[] = [
  { name: "FRA_acme_eXtend_BillingDocuments_QAS" },
  { name: "FRA_acme_eXtend_CustomerMaster_QAS" },
  { name: "FRA_acme_eXtend_IPCSDD_SALES_ORDER_QAS" },
  { name: "FRA_acme_eXtend_SalesPrices_QAS" },
  { name: "FRA_acme_MSSD_SalesPrices_QAS" },
  { name: "FRA_acme_eXtend_SalesPrices_DEV" },
  { name: "orders-prod-v1" },
  { name: "sales.qas.v1" },
];

function run(items: Item[], query: string, keys: (keyof Item & string)[] = ["name"]) {
  return fuzzyFilter(items, { keys, query });
}

describe("fuzzyFilter", () => {
  it("returns all items unchanged when query is empty", () => {
    expect(run(topics, "").results).toEqual(topics);
  });

  it("trims whitespace-only queries to empty", () => {
    expect(run(topics, "   ").results).toEqual(topics);
  });

  it("requires a literal substring match per token (no subsequence fuzziness)", () => {
    const names = run(topics, "qas").results.map((r) => r.name);
    for (const n of names) expect(n.toLowerCase()).toContain("qas");
    const expected = topics
      .filter((t) => t.name.toLowerCase().includes("qas"))
      .map((t) => t.name)
      .sort();
    expect(names.slice().sort()).toEqual(expected);
  });

  it("does not match isolated substrings like 'es' for token 'qas'", () => {
    const names = run(topics, "sales qas").results.map((r) => r.name);
    expect(names).not.toContain("FRA_acme_eXtend_SalesPrices_DEV");
    expect(names).not.toContain("orders-prod-v1");
  });

  it("treats multi-token queries as AND, order-independent", () => {
    const a = run(topics, "sales qas").results.map((r) => r.name).sort();
    const b = run(topics, "qas sales").results.map((r) => r.name).sort();
    expect(a).toEqual(b);
    for (const n of a) {
      expect(n.toLowerCase()).toContain("sales");
      expect(n.toLowerCase()).toContain("qas");
    }
  });

  it("returns zero matches for typos (no character-level fuzziness)", () => {
    expect(run(topics, "salls").results).toEqual([]);
  });

  it("rangesFor returns Fuse indices for the matched key only", () => {
    const res = run(topics, "qas");
    const hit = res.results.find((r) => r.name === "sales.qas.v1")!;
    const ranges = res.rangesFor(hit, "name");
    // 'qas' lives at positions 6..8 in "sales.qas.v1".
    expect(ranges.some(([s, e]) => s === 6 && e === 8)).toBe(true);
    expect(res.rangesFor(hit, "other")).toEqual([]);
  });

  it("ignores leading Fuse extended-search operators pasted by users", () => {
    const a = run(topics, "qas").results.map((r) => r.name).sort();
    const b = run(topics, "'qas").results.map((r) => r.name).sort();
    const c = run(topics, "!qas").results.map((r) => r.name).sort();
    expect(b).toEqual(a);
    expect(c).toEqual(a);
  });

  it("is case-insensitive", () => {
    const lower = run(topics, "qas").results.length;
    const upper = run(topics, "QAS").results.length;
    const mixed = run(topics, "QaS").results.length;
    expect(lower).toBe(upper);
    expect(lower).toBe(mixed);
    expect(lower).toBeGreaterThan(0);
  });

  it("supports multi-key search across multiple fields", () => {
    interface ACL {
      principal: string;
      resource_name: string;
    }
    const acls: ACL[] = [
      { principal: "User:alice", resource_name: "billing-events" },
      { principal: "User:bob", resource_name: "sales-orders" },
      { principal: "User:carol", resource_name: "billing-events" },
    ];
    const out = fuzzyFilter(acls, {
      keys: ["principal", "resource_name"],
      query: "billing",
    });
    const principals = out.results.map((r) => r.principal);
    expect(principals).toEqual(expect.arrayContaining(["User:alice", "User:carol"]));
    expect(principals).not.toContain("User:bob");
  });

  it("rangesFor returns [] when the query is empty", () => {
    const res = run(topics, "");
    expect(res.rangesFor(topics[0], "name")).toEqual([]);
  });
});
