import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from "@tanstack/react-router";

const STORAGE_KEY = "kafkito.private-clusters.v1";

// A private cluster saved in another tab without remembering its passwords.
const STORED = {
  id: "pc_1",
  name: "dev",
  brokers: ["10.0.0.5:9092"],
  auth: { type: "scram-sha-512", username: "alice" },
  tls: { enabled: true },
  schema_registry: {
    url: "https://sr.example.com",
    username: "sr-user",
    credential_required: true,
  },
  remember_credentials: false,
  created_at: 0,
  updated_at: 0,
};

// Session passwords are module state; each test gets a fresh tab.
async function renderGate(cluster: string) {
  vi.resetModules();
  const pc = await import("@/lib/private-clusters");
  const { PrivateClusterPasswordGate } = await import("./PrivateClusterPasswordGate");
  const root = createRootRoute();
  const page = createRoute({
    getParentRoute: () => root,
    path: "/clusters/$cluster",
    component: () => (
      <PrivateClusterPasswordGate cluster={cluster}>
        <p>cluster page</p>
      </PrivateClusterPasswordGate>
    ),
  });
  const fleet = createRoute({
    getParentRoute: () => root,
    path: "/clusters",
    component: () => <p>fleet</p>,
  });
  const router = createRouter({
    routeTree: root.addChildren([page, fleet]),
    history: createMemoryHistory({ initialEntries: [`/clusters/${cluster}`] }),
  });
  render(
    <QueryClientProvider client={new QueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return pc;
}

describe("PrivateClusterPasswordGate", () => {
  beforeEach(() => {
    window.localStorage.clear();
  });
  afterEach(() => {
    cleanup();
  });

  it("asks for the passwords before the cluster's pages render", async () => {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify([STORED]));
    const user = userEvent.setup();
    const pc = await renderGate("dev");

    const dialog = await screen.findByRole("dialog", { name: "Connect to dev" });
    expect(screen.queryByText("cluster page")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Connect" }));
    expect(dialog).toHaveTextContent("Enter the password.");
    expect(dialog).toHaveTextContent("Enter the Schema Registry password.");

    await user.type(screen.getByLabelText("Password for alice"), "kafka-pw");
    await user.type(screen.getByLabelText("Schema Registry password for sr-user"), "sr-pw");
    await user.click(screen.getByRole("button", { name: "Connect" }));

    expect(await screen.findByText("cluster page")).toBeInTheDocument();
    const c = pc.getPrivateClusterByName("dev");
    expect(c?.auth.password).toBe("kafka-pw");
    expect(c?.schema_registry?.password).toBe("sr-pw");
    expect(window.localStorage.getItem(STORAGE_KEY)).not.toContain("kafka-pw");
  });

  it("goes back to the cluster list on Cancel", async () => {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify([STORED]));
    const user = userEvent.setup();
    await renderGate("dev");
    await user.click(await screen.findByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.getByText("fleet")).toBeInTheDocument());
  });

  it("renders a remembered cluster whose registry user has no password", async () => {
    window.localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify([
        {
          ...STORED,
          remember_credentials: undefined,
          auth: { ...STORED.auth, password: "kept" },
          schema_registry: { url: "https://sr.example.com", username: "sr-user" },
        },
      ]),
    );
    await renderGate("dev");
    expect(await screen.findByText("cluster page")).toBeInTheDocument();
  });

  it("renders a cluster that remembers its password or is not private", async () => {
    window.localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify([
        {
          ...STORED,
          remember_credentials: undefined,
          auth: { ...STORED.auth, password: "kept" },
          schema_registry: { ...STORED.schema_registry, password: "kept" },
        },
      ]),
    );
    await renderGate("dev");
    expect(await screen.findByText("cluster page")).toBeInTheDocument();
    cleanup();

    await renderGate("shared");
    expect(await screen.findByText("cluster page")).toBeInTheDocument();
  });
});
