import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, type QueryKey } from "@tanstack/react-query";
import type { PrivateCluster } from "../private-clusters";
import { clusterKey, removePrivateClusterQueries } from "./cluster-key";
import { messageQueries } from "./messages";
import { schemaQueries } from "./schemas";
import { topicQueries } from "./topics";

// The cache identity must follow the cluster the API client actually sends
// the request to (api-client.ts resolves a private cluster by name first).

const NAME = "local";
const PRIVATE: PrivateCluster = {
  id: "pc_local",
  name: NAME,
  brokers: ["10.0.0.1:9092"],
  auth: { type: "none" },
  tls: { enabled: false },
  created_at: 0,
  updated_at: 0,
};

let requests: { path: string; privateHeader: boolean }[] = [];

beforeEach(() => {
  requests = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
      const url = new URL(typeof input === "object" && "url" in input ? input.url : String(input));
      const privateHeader = new Headers(init.headers).has("X-Kafkito-Cluster");
      requests.push({ path: url.pathname, privateHeader });
      const topics = [{ name: privateHeader ? "private-topic" : "shared-topic" }];
      return new Response(JSON.stringify({ topics }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
});

function storePrivate() {
  localStorage.setItem("kafkito.private-clusters.v1", JSON.stringify([PRIVATE]));
}

describe("clusterKey", () => {
  it("is the name for a shared cluster and the id for a private one", () => {
    expect(clusterKey(NAME)).toBe(NAME);
    storePrivate();
    expect(clusterKey(NAME)).toEqual({ private: PRIVATE.id });
  });

  it("never lets a shared and a private cluster of the same name read each other's cache", async () => {
    const qc = new QueryClient();
    // A fresh cache entry would be served without a request if the keys matched.
    const fresh = { staleTime: Number.POSITIVE_INFINITY };

    const shared = await qc.fetchQuery({ ...topicQueries.list(NAME), ...fresh });
    expect(shared.map((t) => t.name)).toEqual(["shared-topic"]);
    expect(requests).toEqual([{ path: `/api/v1/clusters/${NAME}/topics`, privateHeader: false }]);

    storePrivate();
    const priv = await qc.fetchQuery({ ...topicQueries.list(NAME), ...fresh });
    expect(priv.map((t) => t.name)).toEqual(["private-topic"]);
    expect(requests[1]).toEqual({
      path: "/api/v1/clusters/__private__/topics",
      privateHeader: true,
    });

    localStorage.clear();
    const back = await qc.fetchQuery({ ...topicQueries.list(NAME), ...fresh });
    expect(back.map((t) => t.name)).toEqual(["shared-topic"]);
    expect(requests).toHaveLength(2);
  });
});

describe("removePrivateClusterQueries", () => {
  it("drops every entry of that private cluster and nothing else", () => {
    const qc = new QueryClient();
    const keysFor = (name: string): QueryKey[] => [
      topicQueries.list(name).queryKey,
      topicQueries.detail(name, "t").queryKey,
      messageQueries.page(name, "t", {}).queryKey,
      schemaQueries.version(name, "s", "latest").queryKey,
    ];
    const shared = keysFor(NAME);
    const other: PrivateCluster = { ...PRIVATE, id: "pc_other", name: "other" };
    localStorage.setItem("kafkito.private-clusters.v1", JSON.stringify([PRIVATE, other]));
    const mine = keysFor(NAME);
    const theirs = keysFor("other");
    for (const key of [...shared, ...mine, ...theirs]) qc.setQueryData(key, "data");
    // Built while the private cluster held the name, so they differ from `shared`.
    expect(mine[0]).not.toEqual(shared[0]);

    removePrivateClusterQueries(qc, PRIVATE.id);

    for (const key of mine) expect(qc.getQueryData(key), JSON.stringify(key)).toBeUndefined();
    for (const key of [...shared, ...theirs]) expect(qc.getQueryData(key)).toBe("data");
  });
});
