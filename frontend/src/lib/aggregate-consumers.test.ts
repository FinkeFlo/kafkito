import { describe, expect, it } from "vitest";
import {
  aggregateConsumers,
  aggregateConsumersFromInfo,
} from "./aggregate-consumers";
import type { GroupDetail, GroupInfo, GroupOffset } from "./api";

const lagLow = 5;
const lagMid = 50;
const lagHigh = 500;

function offset(
  topic: string,
  partition: number,
  lag: number | undefined,
): GroupOffset {
  return {
    topic,
    partition,
    offset: 0,
    log_end: 0,
    lag: lag as number,
  };
}

function group(overrides: Partial<GroupDetail> & { group_id: string }): GroupDetail {
  return {
    group_id: overrides.group_id,
    state: overrides.state ?? "Stable",
    protocol_type: overrides.protocol_type ?? "consumer",
    coordinator_id: overrides.coordinator_id ?? 1,
    lag: overrides.lag ?? 0,
    lag_known: overrides.lag_known ?? true,
    members: overrides.members ?? [],
    offsets: overrides.offsets ?? [],
  };
}

describe("aggregateConsumers", () => {
  it("returns an empty Map when given no groups (C1)", () => {
    const result = aggregateConsumers([]);

    expect(result.size).toBe(0);
  });

  it("groups offsets per topic and sums lag within a single group across topics (C2)", () => {
    const groupA = group({
      group_id: "group-a",
      offsets: [
        offset("orders", 0, lagLow),
        offset("orders", 1, lagMid),
        offset("payments", 0, lagHigh),
      ],
    });

    const result = aggregateConsumers([groupA]);

    expect(Array.from(result.keys()).sort()).toEqual(["orders", "payments"]);
    expect(result.get("orders")).toEqual([
      { group: "group-a", state: "Stable", members: 0, lag: lagLow + lagMid },
    ]);
    expect(result.get("payments")).toEqual([
      { group: "group-a", state: "Stable", members: 0, lag: lagHigh },
    ]);
  });

  it("creates one entry per group consuming the same topic (C3)", () => {
    const groupA = group({
      group_id: "group-a",
      offsets: [offset("orders", 0, lagHigh)],
    });
    const groupB = group({
      group_id: "group-b",
      offsets: [offset("orders", 0, lagLow)],
    });

    const result = aggregateConsumers([groupA, groupB]);

    const orders = result.get("orders");
    expect(orders).toHaveLength(2);
    expect(orders?.map((e) => e.group).sort()).toEqual(["group-a", "group-b"]);
  });

  it("sums lag across multiple offset rows for the same (group, topic) instead of duplicating entries (C4 / M3 mutation-guard)", () => {
    const groupA = group({
      group_id: "group-a",
      offsets: [
        offset("orders", 0, lagLow),
        offset("orders", 1, lagMid),
        offset("orders", 2, lagHigh),
      ],
    });

    const result = aggregateConsumers([groupA]);

    const orders = result.get("orders");
    expect(orders).toHaveLength(1);
    expect(orders?.[0]?.lag).toBe(lagLow + lagMid + lagHigh);
  });

  it("sorts entries within a topic by lag descending (C5 / M1 mutation-guard)", () => {
    const groupLow = group({
      group_id: "group-low",
      offsets: [offset("orders", 0, lagLow)],
    });
    const groupHigh = group({
      group_id: "group-high",
      offsets: [offset("orders", 0, lagHigh)],
    });
    const groupMid = group({
      group_id: "group-mid",
      offsets: [offset("orders", 0, lagMid)],
    });

    const result = aggregateConsumers([groupLow, groupHigh, groupMid]);

    expect(result.get("orders")?.map((e) => e.lag)).toEqual([
      lagHigh,
      lagMid,
      lagLow,
    ]);
  });

  it("breaks lag ties by group_id ascending (C6 / M2 mutation-guard)", () => {
    const groupZ = group({
      group_id: "zeta",
      offsets: [offset("orders", 0, lagMid)],
    });
    const groupA = group({
      group_id: "alpha",
      offsets: [offset("orders", 0, lagMid)],
    });
    const groupM = group({
      group_id: "mu",
      offsets: [offset("orders", 0, lagMid)],
    });

    const result = aggregateConsumers([groupZ, groupA, groupM]);

    expect(result.get("orders")?.map((e) => e.group)).toEqual([
      "alpha",
      "mu",
      "zeta",
    ]);
  });

  it("falls back to members === 0 when members is not an array (C7 / M4 mutation-guard)", () => {
    const groupA = group({
      group_id: "group-a",
      offsets: [offset("orders", 0, lagLow)],
    });
    const broken = { ...groupA, members: undefined as unknown as GroupDetail["members"] };

    const result = aggregateConsumers([broken]);

    expect(result.get("orders")?.[0]?.members).toBe(0);
  });

  it("counts members from a populated members array", () => {
    const groupA = group({
      group_id: "group-a",
      members: [
        {
          member_id: "m1",
          client_id: "c1",
          client_host: "h1",
          assignments: [],
        },
        {
          member_id: "m2",
          client_id: "c2",
          client_host: "h2",
          assignments: [],
        },
      ],
      offsets: [offset("orders", 0, lagLow)],
    });

    const result = aggregateConsumers([groupA]);

    expect(result.get("orders")?.[0]?.members).toBe(2);
  });

  it("treats a group with nullish offsets as contributing nothing (C8)", () => {
    const groupA = group({
      group_id: "group-a",
      offsets: undefined as unknown as GroupOffset[],
    });

    const result = aggregateConsumers([groupA]);

    expect(result.size).toBe(0);
  });

  it("treats nullish per-offset lag as 0 in the per-topic sum (C9 / M5 mutation-guard)", () => {
    const groupA = group({
      group_id: "group-a",
      offsets: [
        offset("orders", 0, undefined),
        offset("orders", 1, lagLow),
      ],
    });

    const result = aggregateConsumers([groupA]);

    expect(result.get("orders")?.[0]?.lag).toBe(lagLow);
  });
});

describe("aggregateConsumersFromInfo", () => {
  it("returns an empty Map regardless of input (C10 placeholder contract)", () => {
    const groups: GroupInfo[] = [
      {
        group_id: "group-a",
        state: "Stable",
        protocol_type: "consumer",
        coordinator_id: 1,
        members: 3,
        lag: lagHigh,
        lag_known: true,
      },
    ];

    const result = aggregateConsumersFromInfo(groups);

    expect(result.size).toBe(0);
  });
});
