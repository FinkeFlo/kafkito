import { afterEach, describe, expect, it } from "vitest";
import { act, cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from "@tanstack/react-router";
import { getCompareSelection, setCompareSelection } from "@/lib/compare-selection";
import type { MessageRef } from "@/lib/message-reference";
import { CompareAction } from "./CompareAction";
import { CompareTray } from "./CompareTray";

const A: MessageRef = { cluster: "local", topic: "orders", partition: 0, offset: 42 };
const B: MessageRef = { cluster: "other", topic: "orders-copy", partition: 1, offset: 7 };

/** A shell with the tray, a page showing the compare action of `target`, and /compare. */
function renderApp(target: MessageRef, path = "/") {
  const root = createRootRoute({
    component: () => (
      <>
        <Outlet />
        <CompareTray />
      </>
    ),
  });
  const page = createRoute({
    getParentRoute: () => root,
    path: "/",
    component: () => <CompareAction target={target} />,
  });
  const compare = createRoute({
    getParentRoute: () => root,
    path: "/compare",
    component: () => <p>compare page</p>,
  });
  const router = createRouter({
    routeTree: root.addChildren([page, compare]),
    history: createMemoryHistory({ initialEntries: [path] }),
  });
  render(<RouterProvider router={router} />);
  return router;
}

afterEach(() => {
  cleanup();
  act(() => setCompareSelection(null));
});

describe("compare selection flow", () => {
  it("selects a message and shows it in the bar", async () => {
    renderApp(A);
    expect(screen.queryByTestId("compare-tray")).toBeNull();
    await userEvent.click(await screen.findByRole("button", { name: "Select for compare" }));
    expect(getCompareSelection()).toBe("local/orders/0/42");
    const tray = screen.getByRole("complementary", { name: "Message selected for compare" });
    expect(tray).toHaveTextContent("Selected for compare: local / orders · p0 · #42");
    expect(screen.getByText("Selected for compare", { selector: "span" })).toBeVisible();
  });

  it("compares another message with the selected one, remembering where it came from", async () => {
    act(() => setCompareSelection(A));
    const router = renderApp(B);
    await userEvent.click(await screen.findByRole("button", { name: "Compare with selected" }));
    expect(await screen.findByText("compare page")).toBeVisible();
    expect(router.state.location.search).toEqual({
      a: "local/orders/0/42",
      b: "other/orders-copy/1/7",
    });
    expect(router.state.location.state.compareOrigin).toBe(0);
    // The bar steps aside on the compare page.
    expect(screen.queryByTestId("compare-tray")).toBeNull();
  });

  it("lets another message replace the selection", async () => {
    act(() => setCompareSelection(A));
    renderApp(B);
    await userEvent.click(await screen.findByRole("button", { name: "Select this instead" }));
    expect(getCompareSelection()).toBe("other/orders-copy/1/7");
  });

  it("opens the compare page with only message A from the bar", async () => {
    act(() => setCompareSelection(A));
    const router = renderApp(B);
    await userEvent.click(await screen.findByRole("button", { name: "Enter message B" }));
    expect(await screen.findByText("compare page")).toBeVisible();
    expect(router.state.location.search).toEqual({ a: "local/orders/0/42" });
  });

  it("clears the selection from the bar", async () => {
    act(() => setCompareSelection(A));
    renderApp(B);
    await userEvent.click(await screen.findByRole("button", { name: "Clear" }));
    expect(getCompareSelection()).toBeNull();
    expect(screen.queryByTestId("compare-tray")).toBeNull();
    expect(screen.getByRole("button", { name: "Select for compare" })).toBeVisible();
  });
});
