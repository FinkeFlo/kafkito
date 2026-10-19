import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import type { Message } from "@/lib/api";
import { MessageRow } from "./MessageRow";
import type { FieldFilter } from "./use-click-to-filter";

const message: Message = {
  partition: 0,
  offset: 42,
  timestamp_ms: 1_760_000_000_000,
  key: "ord-1",
  key_encoding: "text",
  value: '{"id":1}',
  value_encoding: "json",
  headers: { "trace-id": "t-1", authorization: "***" },
  masked_headers: ["authorization"],
} as Message;

function renderRow(m: Message = message, onFilter: (f: FieldFilter) => void = () => {}) {
  const root = createRootRoute();
  const page = createRoute({
    getParentRoute: () => root,
    path: "/clusters/$cluster/topics/$topic/messages",
    component: () => <MessageRow m={m} onPick={() => {}} onFilter={onFilter} />,
  });
  const router = createRouter({
    routeTree: root.addChildren([page]),
    history: createMemoryHistory({ initialEntries: ["/clusters/local/topics/orders/messages"] }),
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response("{}", { status: 404 })),
  );
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("MessageRow headers", () => {
  it("keeps the headers collapsed until the user opens them", async () => {
    renderRow();
    const row = await screen.findByTestId("message-row");
    await userEvent.click(within(row).getAllByRole("button")[0]);

    const toggle = within(row).getByRole("button", { name: /headers · 2/ });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(within(row).queryAllByTestId("header-line")).toHaveLength(0);

    await userEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    const lines = within(row).getAllByTestId("header-line");
    expect(lines).toHaveLength(2);
    expect(lines[1]).toHaveTextContent("authorization: ***");
    expect(within(lines[1]).getByText("masked")).toBeVisible();
  });

  it("searches by a header's key or value once the headers are open", async () => {
    const onFilter = vi.fn();
    renderRow(message, onFilter);
    const row = await screen.findByTestId("message-row");
    await userEvent.click(within(row).getAllByRole("button")[0]);
    expect(within(row).queryByRole("button", { name: "Search by header trace-id" })).toBeNull();

    await userEvent.click(within(row).getByRole("button", { name: /headers · 2/ }));
    await userEvent.click(within(row).getByRole("button", { name: "Search by header trace-id" }));
    await userEvent.click(within(row).getByRole("button", { name: "Search by trace-id = t-1" }));

    expect(onFilter).toHaveBeenNthCalledWith(1, { kind: "header", key: "trace-id" });
    expect(onFilter).toHaveBeenNthCalledWith(2, { kind: "header", key: "trace-id", value: "t-1" });
    expect(within(row).queryByRole("button", { name: /^Search by authorization =/ })).toBeNull();
  });

  it("says when a message has no headers", async () => {
    renderRow({ ...message, headers: undefined, masked_headers: undefined });
    const row = await screen.findByTestId("message-row");
    await userEvent.click(within(row).getAllByRole("button")[0]);

    expect(within(row).getByText("(no headers)")).toBeVisible();
    expect(within(row).queryByRole("button", { name: /headers ·/ })).toBeNull();
  });

  it("offers the audit snapshot action", async () => {
    renderRow();
    const row = await screen.findByTestId("message-row");
    await userEvent.click(within(row).getAllByRole("button")[0]);

    expect(within(row).getByRole("button", { name: "Audit snapshot" })).toBeVisible();
  });
});
