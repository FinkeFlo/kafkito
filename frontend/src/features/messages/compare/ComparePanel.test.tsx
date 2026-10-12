import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRouter,
} from "@tanstack/react-router";
import { TooltipProvider } from "@/components/ui/Tooltip";
import type { Message, MessagesPage } from "@/lib/api";
import { DEFAULT_COMPARE_OPTIONS } from "@/lib/message-compare";
import type { MessageRef } from "@/lib/message-reference";
import { ComparePanel } from "./ComparePanel";

const fetchMessages = vi.hoisted(() => vi.fn());
const fetchMessageRawBase64 = vi.hoisted(() => vi.fn());

vi.mock("@/lib/api", async (importActual) => {
  const actual = await importActual<typeof import("@/lib/api")>();
  return { ...actual, fetchMessages, fetchMessageRawBase64 };
});

const A: MessageRef = { cluster: "local", topic: "orders", partition: 0, offset: 10 };
const B: MessageRef = { cluster: "local", topic: "orders-copy", partition: 1, offset: 5 };

function msg(ref: MessageRef, over: Partial<Message> = {}): Message {
  return {
    partition: ref.partition,
    offset: ref.offset,
    timestamp_ms: 1_700_000_000_000,
    key_encoding: "text",
    key: "order-1",
    value_encoding: "json",
    value: '{"id":1,"total":12.5}',
    ...over,
  };
}

function page(messages: Message[]): MessagesPage {
  return { cluster: "local", topic: "t", messages, has_more: false } as MessagesPage;
}

/** Serves `records` by topic for the single-message fetch. */
function serve(records: Record<string, Message[]>) {
  fetchMessages.mockImplementation(async (_c: string, topic: string) => page(records[topic] ?? []));
}

function renderPanel(onUseOffset = vi.fn()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  // The result links each message to its topic, so it renders under a router.
  const root = createRootRoute({
    component: () => (
      <ComparePanel
        refA={A}
        refB={B}
        options={DEFAULT_COMPARE_OPTIONS}
        view="semantic"
        onUseOffset={onUseOffset}
      />
    ),
  });
  const router = createRouter({
    routeTree: root,
    history: createMemoryHistory({ initialEntries: ["/compare"] }),
  });
  render(
    <QueryClientProvider client={qc}>
      <TooltipProvider>
        <RouterProvider router={router} />
      </TooltipProvider>
    </QueryClientProvider>,
  );
  return onUseOffset;
}

describe("ComparePanel", () => {
  beforeEach(() => {
    fetchMessages.mockReset();
    fetchMessageRawBase64.mockReset();
  });

  it("fetches each side at its partition and offset", async () => {
    serve({ orders: [msg(A)], "orders-copy": [msg(B)] });
    renderPanel();
    await screen.findByTestId("compare-summary");
    expect(fetchMessages).toHaveBeenCalledWith("local", "orders", {
      partition: 0,
      from: "offset",
      offset: 10,
      limit: 1,
    });
    expect(fetchMessages).toHaveBeenCalledWith("local", "orders-copy", {
      partition: 1,
      from: "offset",
      offset: 5,
      limit: 1,
    });
  });

  it("calls reordered JSON equivalent and names the parts", async () => {
    serve({
      orders: [msg(A)],
      "orders-copy": [msg(B, { value: '{ "total": 12.50, "id": 1 }' })],
    });
    renderPanel();
    const summary = await screen.findByTestId("compare-summary");
    expect(within(summary).getByTestId("verdict")).toHaveTextContent("Equivalent");
    expect(summary).toHaveTextContent("Key: Identical · Value: Equivalent · Headers: Identical");
    expect(screen.getByText(/key order, whitespace and number notation are ignored/)).toBeVisible();
  });

  it("shows a changed value line by line", async () => {
    serve({
      orders: [msg(A)],
      "orders-copy": [msg(B, { value: '{"id":1,"total":13}' })],
    });
    renderPanel();
    const summary = await screen.findByTestId("compare-summary");
    expect(within(summary).getByTestId("verdict")).toHaveTextContent("Different");
    const diff = screen.getByRole("table", { name: /differences in the value/i });
    expect(within(diff).getByText("Removed")).toBeInTheDocument();
    expect(within(diff).getByText("Added")).toBeInTheDocument();
  });

  it("loads the full value of a truncated record before comparing", async () => {
    const full = '{"id":1,"total":12.5}';
    serve({
      orders: [msg(A, { value: '{"id":1,', value_truncated: true, value_size_bytes: 21 })],
      "orders-copy": [msg(B)],
    });
    fetchMessageRawBase64.mockResolvedValue(btoa(full));
    renderPanel();
    const summary = await screen.findByTestId("compare-summary");
    expect(within(summary).getByTestId("verdict")).toHaveTextContent("Identical");
    expect(fetchMessageRawBase64).toHaveBeenCalledWith(
      "local",
      "orders",
      0,
      10,
      expect.anything(),
      true,
    );
  });

  it("offers the next offset when the requested one holds no record", async () => {
    serve({ orders: [msg(A)], "orders-copy": [msg({ ...B, offset: 7 })] });
    const onUseOffset = renderPanel();
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "No record at offset 5 in partition 1 of orders-copy",
    );
    await userEvent.click(screen.getByRole("button", { name: "Use offset 7" }));
    expect(onUseOffset).toHaveBeenCalledWith("B", 7);
  });

  it("names the missing permission when a side is refused", async () => {
    fetchMessages.mockImplementation(async (_c: string, topic: string) => {
      if (topic === "orders") throw new Error("HTTP 403: rbac_denied");
      return page([msg(B)]);
    });
    renderPanel();
    expect(await screen.findByText("No access to message A")).toBeVisible();
    expect(screen.getByRole("alert")).toHaveTextContent("consume permission on topic orders");
    expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
  });

  it("reports a side that fails to load", async () => {
    fetchMessages.mockImplementation(async (_c: string, topic: string) => {
      if (topic === "orders") throw new Error("HTTP 502: kafka unreachable");
      return page([msg(B)]);
    });
    renderPanel();
    expect(await screen.findByText("Message A could not be loaded")).toBeVisible();
    expect(screen.getByText("HTTP 502: kafka unreachable")).toBeVisible();
    expect(screen.getByRole("button", { name: "Retry" })).toBeVisible();
  });
});
