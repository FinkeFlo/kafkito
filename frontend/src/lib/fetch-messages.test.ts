import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// Stub fetch so we can assert the exact request path that fetchMessages
// builds from its params (query-string serialization).
const fetchMock = vi.fn();

import { fetchMessageCount, fetchMessages } from "./api";

function okResponse() {
  return new Response(JSON.stringify({ messages: [] }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

function lastPath(): string {
  const call = fetchMock.mock.calls.at(-1);
  return String(call?.[0]);
}

beforeEach(() => {
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  fetchMock.mockReset();
  vi.unstubAllGlobals();
});

describe("fetchMessages query serialization", () => {
  it("sends a single offset when one partition is selected", async () => {
    fetchMock.mockImplementation(async () => okResponse());
    await fetchMessages("c1", "t1", { partition: 0, from: "offset", offset: 223 });
    const path = lastPath();
    expect(path).toContain("partition=0");
    expect(path).toContain("from=offset");
    expect(path).toContain("offset=223");
    expect(path).not.toContain("partition_offsets");
  });

  it("serializes partitionOffsets as p:o pairs for all partitions", async () => {
    fetchMock.mockImplementation(async () => okResponse());
    await fetchMessages("c1", "t1", {
      from: "offset",
      partitionOffsets: { 0: 5, 1: 5, 2: 5 },
    });
    const path = lastPath();
    const qs = path.split("?")[1] ?? "";
    const params = new URLSearchParams(qs);
    expect(params.get("from")).toBe("offset");
    expect(params.get("partition_offsets")).toBe("0:5,1:5,2:5");
    // partition = all must not be pinned to a single partition.
    expect(params.has("partition")).toBe(false);
    expect(params.has("offset")).toBe(false);
  });

  it("omits partition_offsets when the map is empty", async () => {
    fetchMock.mockImplementation(async () => okResponse());
    await fetchMessages("c1", "t1", { from: "offset", partitionOffsets: {} });
    expect(lastPath()).not.toContain("partition_offsets");
  });
});

describe("fetchMessageCount query serialization", () => {
  it("omits partition for all-partitions requests", async () => {
    fetchMock.mockImplementation(async () => okResponse());
    await fetchMessageCount("c1", "t1", { from_ts_ms: 100, to_ts_ms: 200 });
    const params = new URLSearchParams(lastPath().split("?")[1] ?? "");
    expect(params.has("partition")).toBe(false);
    expect(params.get("from_ts_ms")).toBe("100");
    expect(params.get("to_ts_ms")).toBe("200");
  });

  it("serializes a concrete partition when selected", async () => {
    fetchMock.mockImplementation(async () => okResponse());
    await fetchMessageCount("c1", "t1", { partition: 3 });
    const params = new URLSearchParams(lastPath().split("?")[1] ?? "");
    expect(params.get("partition")).toBe("3");
  });
});
