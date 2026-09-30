import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import type { ClusterInfo } from "@/lib/api";
import { TestConnectionResult } from "./TestConnectionResult";

const base: ClusterInfo = {
  name: "",
  reachable: true,
  is_prod: false,
  auth_type: "none",
  tls: false,
  schema_registry: false,
};

afterEach(cleanup);

describe("TestConnectionResult", () => {
  it("reports a reachable cluster", () => {
    render(<TestConnectionResult outcome={{ kind: "probed", info: base }} />);
    expect(screen.getByRole("status")).toHaveTextContent("OK — reachable (none, TLS: no)");
  });

  // Issue #126: the seed answered, but a broker advertises a blocked address.
  it("lists each advertised broker that cannot be reached", () => {
    const info: ClusterInfo = {
      ...base,
      reachable: false,
      error:
        "some advertised brokers cannot be reached: node 1: destination not allowed; node 2: connection timed out",
      broker_issues: [
        {
          node_id: 1,
          host: "localhost",
          port: 39092,
          reason: "blocked",
          error: "destination not allowed",
          error_class: "blocked",
        },
        {
          node_id: 2,
          host: "fd00::7",
          port: 9092,
          reason: "unreachable",
          error: "connection timed out",
          error_class: "timeout",
        },
      ],
    };
    render(<TestConnectionResult outcome={{ kind: "probed", info }} />);

    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent(/not every broker the cluster advertises can be reached/);
    const items = within(within(alert).getByRole("list", { name: "Broker issues" })).getAllByRole(
      "listitem",
    );
    expect(items.map((li) => li.textContent)).toEqual([
      "Broker 1 advertises localhost:39092, which is not allowed for private clusters (it resolves to a loopback, link-local, multicast or unspecified address).",
      "Broker 2 advertises [fd00::7]:9092, which did not answer: connection timed out",
    ]);
    expect(alert).not.toHaveTextContent(/^Unreachable: /);
  });

  it("reports a seed timeout with the cold-DNS hint", () => {
    const info: ClusterInfo = {
      ...base,
      reachable: false,
      error: "connection timed out",
      error_class: "timeout",
    };
    render(<TestConnectionResult outcome={{ kind: "probed", info }} />);
    expect(screen.getByRole("alert")).toHaveTextContent(
      /^Unreachable: connection timed out — first probe is slow on cold broker DNS/,
    );
  });

  it("reports any other seed failure by its text alone", () => {
    const info: ClusterInfo = {
      ...base,
      reachable: false,
      error: "connection refused",
      error_class: "refused",
    };
    render(<TestConnectionResult outcome={{ kind: "probed", info }} />);
    expect(screen.getByRole("alert")).toHaveTextContent(/^Unreachable: connection refused$/);
  });

  it("reports a request error", () => {
    render(<TestConnectionResult outcome={{ kind: "error", message: "HTTP 400: nope" }} />);
    expect(screen.getByRole("alert")).toHaveTextContent("Error: HTTP 400: nope");
  });

  // Brokers beyond the probe cap are not checked; say so, even when the
  // checked ones all answered.
  it("notes advertised brokers that were not checked", () => {
    const info: ClusterInfo = { ...base, brokers_skipped: 3 };
    render(<TestConnectionResult outcome={{ kind: "probed", info }} />);
    const status = screen.getByRole("status");
    expect(status).toHaveTextContent("OK — reachable (none, TLS: no)");
    expect(status).toHaveTextContent("3 more brokers were not checked.");
  });

  it("notes a single unchecked broker next to the broker issues", () => {
    const info: ClusterInfo = {
      ...base,
      reachable: false,
      brokers_skipped: 1,
      broker_issues: [
        {
          node_id: 4,
          host: "10.0.0.4",
          port: 9092,
          reason: "unreachable",
          error: "connection timed out",
          error_class: "timeout",
        },
      ],
    };
    render(<TestConnectionResult outcome={{ kind: "probed", info }} />);
    expect(screen.getByRole("alert")).toHaveTextContent("1 more broker was not checked.");
  });

  it("adds no note when every advertised broker was checked", () => {
    const info: ClusterInfo = { ...base, brokers_skipped: 0 };
    render(<TestConnectionResult outcome={{ kind: "probed", info }} />);
    expect(screen.getByRole("status")).not.toHaveTextContent(/not checked/);
  });
});
