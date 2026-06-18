import { afterEach, describe, expect, it, vi } from "vitest";

// Mock the low-level HTTP layer so we can assert the exact request path that
// fetchMessages builds from its params (query-string serialization).
const fetchAPI = vi.fn();
vi.mock("./api-http", () => ({
  clusterPath: (cluster: string, subpath: string) =>
    `/api/v1/clusters/${encodeURIComponent(cluster)}${subpath.startsWith("/") ? subpath : "/" + subpath}`,
  fetchAPI: (...args: unknown[]) => fetchAPI(...args),
}));

import { fetchMessages } from "./api";

function okResponse() {
  return {
    ok: true,
    status: 200,
    json: async () => ({ messages: [] }),
  } as unknown as Response;
}

function lastPath(): string {
  const call = fetchAPI.mock.calls.at(-1);
  return call?.[1] as string;
}

afterEach(() => {
  fetchAPI.mockReset();
});

describe("fetchMessages query serialization", () => {
  it("sends a single offset when one partition is selected", async () => {
    fetchAPI.mockResolvedValue(okResponse());
    await fetchMessages("c1", "t1", { partition: 0, from: "offset", offset: 223 });
    const path = lastPath();
    expect(path).toContain("partition=0");
    expect(path).toContain("from=offset");
    expect(path).toContain("offset=223");
    expect(path).not.toContain("partition_offsets");
  });

  it("serializes partitionOffsets as p:o pairs for all partitions", async () => {
    fetchAPI.mockResolvedValue(okResponse());
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
    fetchAPI.mockResolvedValue(okResponse());
    await fetchMessages("c1", "t1", { from: "offset", partitionOffsets: {} });
    expect(lastPath()).not.toContain("partition_offsets");
  });
});
