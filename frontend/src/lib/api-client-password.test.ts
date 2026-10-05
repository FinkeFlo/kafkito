import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as api from "./api";
import { PasswordRequiredError } from "./private-clusters";

// A private cluster saved in another tab without remembering its password.
const STORED = {
  id: "pc_nopw",
  name: "dev",
  brokers: ["10.0.0.5:9092"],
  auth: { type: "scram-sha-512", username: "alice" },
  tls: { enabled: true },
  remember_credentials: false,
  created_at: 0,
  updated_at: 0,
};

describe("a request for a private cluster without its password", () => {
  const fetchSpy = vi.fn(async () => new Response("[]", { status: 200 }));

  beforeEach(() => {
    window.localStorage.setItem("kafkito.private-clusters.v1", JSON.stringify([STORED]));
    vi.stubGlobal("fetch", fetchSpy);
  });
  afterEach(() => {
    window.localStorage.clear();
    vi.unstubAllGlobals();
    fetchSpy.mockClear();
  });

  it("fails with PasswordRequiredError and is never sent", async () => {
    await expect(api.fetchTopics("dev")).rejects.toBeInstanceOf(PasswordRequiredError);
    expect(fetchSpy).not.toHaveBeenCalled();
  });
});
