import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider, type QueryKey } from "@tanstack/react-query";
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRouter,
} from "@tanstack/react-router";
import { toast } from "sonner";
import type { PrivateCluster } from "@/lib/private-clusters";
import { Route as SettingsRoute } from "@/routes/settings.clusters";
import { groupQueries } from "./groups";
import { messageQueries } from "./messages";
import { topicQueries } from "./topics";

// The private-cluster settings page: names that collide with a server
// cluster are refused (and flagged on entries saved before), and editing or
// deleting a private cluster drops its cached queries.

const fetchClusters = vi.hoisted(() => vi.fn());

vi.mock("@/lib/api", async (importActual) => {
  const actual = await importActual<typeof import("@/lib/api")>();
  return { ...actual, fetchClusters };
});

const SHARED = "local";
const STORAGE_KEY = "kafkito.private-clusters.v1";
const HINT = "Same name as a server cluster – rename it to reach both";

function privateCluster(id: string, name: string): PrivateCluster {
  return {
    id,
    name,
    brokers: ["10.0.0.1:9092"],
    auth: { type: "none" },
    tls: { enabled: false },
    created_at: 0,
    updated_at: 0,
  };
}

function store(...clusters: PrivateCluster[]) {
  localStorage.setItem(STORAGE_KEY, JSON.stringify(clusters));
}

function renderSettings(qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
  const root = createRootRoute();
  const settings = SettingsRoute.update({
    id: "/settings/clusters",
    path: "/settings/clusters",
    getParentRoute: () => root,
    // Same cast as routeTree.gen.ts: update() is typed for generated trees.
  } as any);
  const router = createRouter({
    routeTree: root.addChildren([settings]),
    history: createMemoryHistory({ initialEntries: ["/settings/clusters"] }),
  });
  render(
    <QueryClientProvider client={qc}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return qc;
}

function row(name: string) {
  return screen.getByRole("row", { name: new RegExp(`Select ${name} for export`) });
}

function stored(): PrivateCluster[] {
  return JSON.parse(localStorage.getItem(STORAGE_KEY) ?? "[]");
}

async function openAddForm(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByRole("button", { name: "Add cluster" }));
  return screen.findByRole("dialog", { name: "Add private cluster" });
}

beforeEach(() => {
  fetchClusters.mockReset().mockResolvedValue([
    {
      name: SHARED,
      reachable: true,
      is_prod: false,
      auth_type: "none",
      tls: false,
      schema_registry: false,
    },
  ]);
});

afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.restoreAllMocks();
});

describe("private cluster settings", () => {
  it("refuses a name that a server cluster already has", async () => {
    const user = userEvent.setup();
    renderSettings();
    await waitFor(() => expect(fetchClusters).toHaveBeenCalled());

    await user.click(screen.getByRole("button", { name: "Add cluster" }));
    const form = await screen.findByRole("dialog", { name: "Add private cluster" });
    const name = within(form).getByPlaceholderText("my-dev-cluster");
    await user.type(within(form).getByPlaceholderText("host1:9092, host2:9092"), "10.0.0.1:9092");
    await user.type(name, ` ${SHARED} `);

    const error = await within(form).findByText(
      `A server cluster is already named "${SHARED}". Choose another name.`,
    );
    expect(name).toHaveAttribute("aria-invalid", "true");
    expect(name.getAttribute("aria-describedby")).toBe(error.closest("p")?.id);
    expect(error.closest("[aria-live]")).toHaveAttribute("aria-live", "polite");
    expect(within(form).getByRole("button", { name: "Save" })).toBeDisabled();

    await user.type(name, "-2");
    expect(within(form).queryByText(/already named/)).not.toBeInTheDocument();
    expect(name).not.toHaveAttribute("aria-invalid");
    expect(within(form).getByRole("button", { name: "Save" })).toBeEnabled();
  });

  it("flags a stored private cluster that collides, until it is renamed", async () => {
    const user = userEvent.setup();
    store(privateCluster("pc_old", SHARED), privateCluster("pc_ok", "mine"));
    renderSettings();

    await screen.findByRole("row", { name: new RegExp(`Select ${SHARED} for export`) });
    expect(await within(row(SHARED)).findByText(HINT)).toBeInTheDocument();
    expect(within(row(SHARED)).getByRole("img", { name: "Warning" })).toBeInTheDocument();
    expect(within(row("mine")).queryByText(HINT)).not.toBeInTheDocument();

    await user.click(within(row(SHARED)).getByRole("button", { name: "Edit" }));
    const form = await screen.findByRole("dialog", { name: "Edit private cluster" });
    expect(within(form).getByRole("button", { name: "Save" })).toBeDisabled();
    const name = within(form).getByPlaceholderText("my-dev-cluster");
    await user.clear(name);
    await user.type(name, "local-private");
    await user.click(within(form).getByRole("button", { name: "Save" }));

    expect(
      await screen.findByRole("row", { name: /Select local-private for export/ }),
    ).toBeVisible();
    expect(screen.queryByText(HINT)).not.toBeInTheDocument();
  });

  it("drops the cached queries of a private cluster when it is edited or deleted", async () => {
    const user = userEvent.setup();
    store(privateCluster("pc_a", "a"), privateCluster("pc_b", "b"));
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const keysFor = (cluster: string): QueryKey[] => [
      topicQueries.list(cluster).queryKey,
      groupQueries.list(cluster).queryKey,
      messageQueries.page(cluster, "t", {}).queryKey,
    ];
    const [a, b, shared] = [keysFor("a"), keysFor("b"), keysFor(SHARED)];
    for (const key of [...a, ...b, ...shared]) qc.setQueryData(key, "cached");
    renderSettings(qc);

    await user.click(
      within(await screen.findByRole("row", { name: /Select a for export/ })).getByRole("button", {
        name: "Edit",
      }),
    );
    const form = await screen.findByRole("dialog", { name: "Edit private cluster" });
    const brokers = within(form).getByPlaceholderText("host1:9092, host2:9092");
    await user.clear(brokers);
    await user.type(brokers, "10.0.0.2:9092");
    await user.click(within(form).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    for (const key of a) expect(qc.getQueryData(key)).toBeUndefined();
    for (const key of [...b, ...shared]) expect(qc.getQueryData(key)).toBe("cached");

    await user.click(within(row("b")).getByRole("button", { name: "Delete" }));
    const confirm = await screen.findByRole("dialog", { name: "Delete private cluster?" });
    await user.click(within(confirm).getByRole("button", { name: "Delete" }));

    await waitFor(() => expect(qc.getQueryData(b[0])).toBeUndefined());
    for (const key of b) expect(qc.getQueryData(key)).toBeUndefined();
    for (const key of shared) expect(qc.getQueryData(key)).toBe("cached");
  });
});

// TLS to the broker is on for new clusters; the form warns about settings
// that expose credentials or data, without blocking Save or Test connection.
describe("private cluster transport security", () => {
  it("turns TLS on for a new cluster", async () => {
    const user = userEvent.setup();
    renderSettings();
    const form = await openAddForm(user);
    expect(within(form).getByLabelText("Enabled")).toBeChecked();
    expect(within(form).getByLabelText("Skip verify")).not.toBeChecked();
    expect(within(form).queryByRole("alert")).not.toBeInTheDocument();

    await user.type(within(form).getByPlaceholderText("my-dev-cluster"), "secure");
    await user.type(within(form).getByPlaceholderText("host1:9092, host2:9092"), "10.0.0.1:9093");
    await user.click(within(form).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(stored()).toEqual([
      expect.objectContaining({
        name: "secure",
        tls: { enabled: true, insecure_skip_verify: false },
      }),
    ]);
  });

  it("warns about an unencrypted connection and still saves it", async () => {
    const user = userEvent.setup();
    renderSettings();
    const form = await openAddForm(user);
    const tls = within(form).getByLabelText("Enabled");

    await user.click(tls);
    expect(within(form).getByRole("alert")).toHaveTextContent(
      "Without TLS, all data travels unencrypted.",
    );

    await user.selectOptions(within(form).getByLabelText("Auth type"), "plain");
    const alert = within(form).getByRole("alert");
    expect(alert).toHaveTextContent(
      "SASL/PLAIN without TLS sends the username and password in cleartext, and all data as well.",
    );
    expect(within(alert).getByRole("img", { name: "Warning" })).toBeInTheDocument();

    await user.click(tls);
    expect(within(form).queryByRole("alert")).not.toBeInTheDocument();
    await user.click(tls);

    await user.type(within(form).getByPlaceholderText("my-dev-cluster"), "cleartext");
    await user.type(within(form).getByPlaceholderText("host1:9092, host2:9092"), "10.0.0.1:9092");
    await user.type(within(form).getByLabelText(/^Username/), "alice");
    await user.type(within(form).getByLabelText(/^Password/), "secret");
    expect(within(form).getByRole("alert")).toHaveTextContent(/username and password in cleartext/);
    expect(within(form).getByRole("button", { name: "Test connection" })).toBeEnabled();
    await user.click(within(form).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(stored()).toEqual([
      expect.objectContaining({
        name: "cleartext",
        auth: { type: "plain", username: "alice", password: "secret" },
        tls: { enabled: false, insecure_skip_verify: false },
      }),
    ]);
  });

  // Clicking the nested Skip verify checkbox is not exercised here: happy-dom
  // also activates the wrapping TLS field label, which browsers do not do.
  it("keeps the stored TLS settings of existing clusters and warns about them", async () => {
    const user = userEvent.setup();
    store(privateCluster("pc_old", "old"), {
      ...privateCluster("pc_unverified", "unverified"),
      tls: { enabled: true, insecure_skip_verify: true },
    });
    renderSettings();

    await user.click(
      within(await screen.findByRole("row", { name: /Select old for export/ })).getByRole(
        "button",
        { name: "Edit" },
      ),
    );
    let form = await screen.findByRole("dialog", { name: "Edit private cluster" });
    expect(within(form).getByLabelText("Enabled")).not.toBeChecked();
    expect(within(form).getByRole("alert")).toHaveTextContent(
      "Without TLS, all data travels unencrypted.",
    );
    expect(within(form).getByRole("button", { name: "Save" })).toBeEnabled();
    await user.click(within(form).getByRole("button", { name: "Cancel" }));

    await user.click(within(row("unverified")).getByRole("button", { name: "Edit" }));
    form = await screen.findByRole("dialog", { name: "Edit private cluster" });
    expect(within(form).getByLabelText("Enabled")).toBeChecked();
    expect(within(form).getByLabelText("Skip verify")).toBeChecked();
    expect(within(form).getByRole("alert")).toHaveTextContent(
      "With Skip verify, the broker's certificate is not checked, so the connection can be intercepted.",
    );
    expect(within(form).getByRole("button", { name: "Save" })).toBeEnabled();
  });
});

// Exports are encrypted with a passphrase and import again once it is
// entered; plaintext exports from before still import.
describe("private cluster export and import", () => {
  const PASSPHRASE = "correct horse battery staple";

  function captureDownloads() {
    const blobs: Blob[] = [];
    const names: string[] = [];
    vi.spyOn(URL, "createObjectURL").mockImplementation((blob) => {
      blobs.push(blob as Blob);
      return "blob:export";
    });
    vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
      this: HTMLAnchorElement,
    ) {
      names.push(this.download);
    });
    return { blobs, names };
  }

  async function upload(user: ReturnType<typeof userEvent.setup>, name: string, text: string) {
    const input = document.querySelector<HTMLInputElement>('input[type="file"]');
    if (!input) throw new Error("file input not found");
    await user.upload(input, new File([text], name, { type: "application/json" }));
  }

  it("encrypts the export and imports it again with the passphrase", async () => {
    const user = userEvent.setup();
    const success = vi.spyOn(toast, "success");
    const downloads = captureDownloads();
    const payments: PrivateCluster = {
      ...privateCluster("pc_payments", "payments-dev"),
      auth: { type: "scram-sha-512", username: "svc-payments", password: "hunter2-hunter2" },
      tls: { enabled: true, insecure_skip_verify: false },
    };
    store(payments, privateCluster("pc_other", "other"));
    renderSettings();

    await user.click(
      await screen.findByRole("checkbox", { name: "Select payments-dev for export" }),
    );
    await user.click(screen.getByRole("button", { name: "Export 1 selected" }));
    const exportDialog = await screen.findByRole("dialog", { name: "Export private clusters" });
    expect(within(exportDialog).getByRole("status")).toHaveTextContent(
      /^The file contains 1 cluster, including their credentials/,
    );
    await user.type(within(exportDialog).getByLabelText("Passphrase"), PASSPHRASE);
    await user.type(within(exportDialog).getByLabelText("Confirm passphrase"), PASSPHRASE);
    await user.click(within(exportDialog).getByRole("button", { name: "Export" }));

    await waitFor(() => expect(downloads.blobs).toHaveLength(1), { timeout: 5_000 });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(downloads.names).toEqual([
      expect.stringMatching(/^kafkito-private-clusters-\d{4}-\d{2}-\d{2}\.json$/),
    ]);
    expect(downloads.blobs[0].type).toBe("application/json");
    const file = await downloads.blobs[0].text();
    expect(JSON.parse(file)).toMatchObject({
      format: "kafkito.private-clusters.export",
      version: 2,
      kdf: { iterations: 600_000 },
    });
    for (const secret of ["hunter2-hunter2", "svc-payments", "payments-dev", "10.0.0.1"]) {
      expect(file).not.toContain(secret);
    }

    localStorage.clear();
    await upload(user, "clusters.json", file);
    const importDialog = await screen.findByRole("dialog", { name: "Import private clusters" });
    expect(importDialog).toHaveTextContent("clusters.json is encrypted.");
    const passphrase = within(importDialog).getByLabelText("Passphrase");
    await user.type(passphrase, "not the passphrase");
    await user.click(within(importDialog).getByRole("button", { name: "Import" }));
    expect(
      await within(importDialog).findByRole("alert", {}, { timeout: 5_000 }),
    ).toHaveTextContent("Wrong passphrase or damaged file.");
    expect(stored()).toEqual([]);

    await user.clear(passphrase);
    await user.type(passphrase, PASSPHRASE);
    await user.click(within(importDialog).getByRole("button", { name: "Import" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument(), {
      timeout: 5_000,
    });
    expect(stored()).toEqual([payments]);
    expect(success).toHaveBeenCalledExactlyOnceWith("Imported: 1 added, 0 updated, 0 skipped", {
      description: undefined,
    });
  });

  it("still imports a plaintext export and suggests exporting again", async () => {
    const user = userEvent.setup();
    const success = vi.spyOn(toast, "success");
    renderSettings();
    await screen.findByRole("button", { name: "Import JSON" });

    const legacy = privateCluster("pc_legacy", "legacy");
    const bundle = {
      schema: "kafkito.private-clusters/v1",
      exported_at: "2026-01-01T00:00:00.000Z",
      clusters: [legacy],
    };
    await upload(user, "old-export.json", JSON.stringify(bundle, null, 2));

    expect(await screen.findByRole("row", { name: /Select legacy for export/ })).toBeVisible();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(stored()).toEqual([legacy]);
    expect(success).toHaveBeenCalledExactlyOnceWith("Imported: 1 added, 0 updated, 0 skipped", {
      description:
        "This file was not encrypted. Export again for an encrypted copy, and delete the unencrypted file.",
    });
  });

  it("says that stored credentials are unencrypted and exports are encrypted", async () => {
    const user = userEvent.setup();
    renderSettings();
    const form = await openAddForm(user);
    expect(form).toHaveTextContent(
      "Credentials are stored unencrypted in this browser's localStorage. Exports are encrypted with a passphrase you choose",
    );
  });
});
