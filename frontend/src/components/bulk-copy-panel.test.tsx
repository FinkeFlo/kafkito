import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BulkCopyPanel } from "./bulk-copy-panel";
import type { CopyProgressEvent } from "@/lib/api";
import type { ClusterListItem } from "@/lib/use-cluster";

const copyMessages = vi.hoisted(() => vi.fn());
const fetchTopics = vi.hoisted(() => vi.fn());
const useCluster = vi.hoisted(() => vi.fn());

vi.mock("@/lib/api", async (importActual) => {
  const actual = await importActual<typeof import("@/lib/api")>();
  return { ...actual, copyMessages, fetchTopics };
});

// useCluster pulls in the router; the panel only needs the cluster list.
vi.mock("@/lib/use-cluster", () => ({ useCluster }));

const abort = vi.fn();

function cluster(name: string, is_prod = false): ClusterListItem {
  return {
    source: "shared",
    name,
    reachable: true,
    is_prod,
    auth_type: "none",
    tls: false,
    schema_registry: false,
  };
}

function renderPanel(clusters: ClusterListItem[] = [cluster("dest-a")]) {
  useCluster.mockReturnValue({
    cluster: "src",
    clusters,
    setCluster: vi.fn(),
    isLoading: false,
    isUnknownCluster: false,
    defaultCluster: clusters[0]?.name ?? null,
  });
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <BulkCopyPanel srcCluster="src" srcTopic="src-topic" partitions={[0, 1]} />
    </QueryClientProvider>,
  );
}

/** Pushes an SSE progress event through the `onProgress` callback the panel handed to copyMessages. */
function emit(ev: CopyProgressEvent) {
  const onProgress = copyMessages.mock.calls.at(-1)?.[4] as (
    e: CopyProgressEvent,
  ) => void;
  act(() => onProgress(ev));
}

/**
 * The destination cluster <select>; located via one of its options because the
 * panel's labels are not bound to their controls via htmlFor.
 */
function destClusterSelect(name = "dest-a"): HTMLSelectElement {
  const option = screen.getByRole("option", { name: new RegExp(`^${name}`) });
  return option.closest("select") as HTMLSelectElement;
}

function startButton() {
  return screen.getByRole("button", { name: /start copy/i });
}

async function startCopy(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByPlaceholderText("topic-name"), "dest-topic");
  await user.click(startButton());
}

beforeEach(() => {
  copyMessages.mockReset();
  abort.mockReset();
  copyMessages.mockReturnValue(abort);
  fetchTopics.mockResolvedValue([]);
});

afterEach(cleanup);

describe("BulkCopyPanel skipped counter", () => {
  it("labels skipped records without claiming a cause and explains why in a tooltip", async () => {
    const user = userEvent.setup();
    renderPanel();
    await startCopy(user);

    emit({ copied: 10, skipped: 3 });

    const label = screen.getByText(/3 skipped/);
    expect(label).toHaveTextContent("not reproducible byte-for-byte");
    // The old wording blamed the encoding; skips now also cover masked records.
    expect(screen.queryByText(/unsupported encoding/i)).not.toBeInTheDocument();
    // The two real reasons stay discoverable without cluttering the line.
    expect(label.getAttribute("title")).toMatch(/schema registry/i);
    expect(label.getAttribute("title")).toMatch(/masking/i);
  });

  it("omits the skipped counter when nothing was skipped", async () => {
    const user = userEvent.setup();
    renderPanel();
    await startCopy(user);

    emit({ copied: 10, skipped: 0 });
    expect(screen.queryByText(/skipped/i)).not.toBeInTheDocument();

    emit({ copied: 12 });
    expect(screen.queryByText(/skipped/i)).not.toBeInTheDocument();
  });
});

describe("BulkCopyPanel errors", () => {
  it("renders a human-readable message when the server rate-limits the copy", async () => {
    const user = userEvent.setup();
    renderPanel();
    await startCopy(user);

    emit({
      copied: 0,
      done: true,
      error: "HTTP 429: too many concurrent copy jobs",
    });

    const msg = screen.getByText(/too many copies are running right now/i);
    expect(msg).toBeInTheDocument();
    // The raw status line is kept as detail, not shown as the message.
    expect(screen.queryByText(/HTTP 429/)).not.toBeInTheDocument();
    expect(msg.getAttribute("title")).toBe(
      "HTTP 429: too many concurrent copy jobs",
    );
  });

  it("still surfaces other errors verbatim", async () => {
    const user = userEvent.setup();
    renderPanel();
    await startCopy(user);

    emit({ copied: 0, done: true, error: "HTTP 500: broker unavailable" });

    expect(
      screen.getByText(/Error: HTTP 500: broker unavailable/),
    ).toBeInTheDocument();
  });
});

describe("BulkCopyPanel controls while running", () => {
  it("disables every input including the destination cluster select, then re-enables on done", async () => {
    const user = userEvent.setup();
    renderPanel([cluster("dest-a"), cluster("dest-b")]);
    await startCopy(user);

    emit({ copied: 1 });

    // Changing the destination mid-run cannot affect the running server job,
    // so the select must be locked like every other control.
    expect(destClusterSelect()).toBeDisabled();
    expect(screen.getByPlaceholderText("topic-name")).toBeDisabled();
    expect(screen.getByPlaceholderText("no limit")).toBeDisabled();
    expect(screen.getByRole("button", { name: /last 1h/i })).toBeDisabled();
    expect(screen.getByRole("button", { name: /^clear$/i })).toBeDisabled();
    expect(screen.getByRole("checkbox")).toBeDisabled();
    expect(
      screen.getByRole("button", { name: /stop/i }),
    ).toBeInTheDocument();

    emit({ copied: 5, done: true });

    expect(destClusterSelect()).toBeEnabled();
    expect(screen.getByPlaceholderText("topic-name")).toBeEnabled();
    expect(screen.getByPlaceholderText("no limit")).toBeEnabled();
    expect(screen.getByRole("checkbox")).toBeEnabled();
    expect(startButton()).toBeEnabled();
  });

  it("aborts the SSE stream when Stop is clicked and does not claim an instant halt", async () => {
    const user = userEvent.setup();
    renderPanel();
    await startCopy(user);
    emit({ copied: 7 });

    await user.click(screen.getByRole("button", { name: /stop/i }));

    expect(abort).toHaveBeenCalledTimes(1);
    // Aborting only closes the browser's stream; the job stops when its next
    // progress write fails, so the UI must not read as "everything stopped here".
    expect(
      screen.getByText(/the server may copy a few more before it notices/i),
    ).toBeInTheDocument();
    expect(screen.queryByText(/^Copying…/)).not.toBeInTheDocument();
    expect(startButton()).toBeEnabled();
  });
});

describe("BulkCopyPanel start gating", () => {
  it("blocks Start until a destination topic is entered", async () => {
    const user = userEvent.setup();
    renderPanel();

    expect(startButton()).toBeDisabled();

    await user.type(screen.getByPlaceholderText("topic-name"), "dest-topic");
    expect(startButton()).toBeEnabled();

    await user.clear(screen.getByPlaceholderText("topic-name"));
    expect(startButton()).toBeDisabled();
    expect(copyMessages).not.toHaveBeenCalled();
  });

  it("hints that the time range is bounded by the copy start time", () => {
    renderPanel();

    const hint = screen.getByText(/empty To = the moment the copy starts/i);
    expect(hint).toBeInTheDocument();
    // "leave empty for all" was wrong: an open-ended To would tail forever.
    expect(screen.queryByText(/leave empty for all/i)).not.toBeInTheDocument();
  });
});

describe("BulkCopyPanel production destination", () => {
  it("requires confirmation before copying to a prod cluster", async () => {
    const user = userEvent.setup();
    renderPanel([cluster("prod-a", true)]);

    await user.type(screen.getByPlaceholderText("topic-name"), "dest-topic");
    await user.click(startButton());

    expect(copyMessages).not.toHaveBeenCalled();
    expect(screen.getByText(/production cluster warning/i)).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /copy anyway/i }));

    await waitFor(() => expect(copyMessages).toHaveBeenCalledTimes(1));
    const [srcCluster, srcTopic, req, confirmedProd] = copyMessages.mock.calls[0];
    expect(srcCluster).toBe("src");
    expect(srcTopic).toBe("src-topic");
    expect(req.dest_cluster).toBe("prod-a");
    expect(req.dest_topic).toBe("dest-topic");
    expect(confirmedProd).toBe(true);
  });

  it("starts immediately for a non-prod destination", async () => {
    const user = userEvent.setup();
    renderPanel();
    await startCopy(user);

    expect(copyMessages).toHaveBeenCalledTimes(1);
    expect(copyMessages.mock.calls[0][3]).toBe(false);
    expect(
      screen.queryByText(/production cluster warning/i),
    ).not.toBeInTheDocument();
  });
});
