import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HeaderLines, RecordKey } from "./FilterableFields";

afterEach(cleanup);

describe("HeaderLines", () => {
  const headers = { "x-trace": "t-1", authorization: "[redacted]", "x-empty": "" };

  it("searches by header when its key is clicked and by value when its value is", async () => {
    const onFilter = vi.fn();
    const user = userEvent.setup();
    render(<HeaderLines headers={headers} masked={new Set()} onFilter={onFilter} />);

    await user.click(screen.getByRole("button", { name: "Search by header x-trace" }));
    expect(onFilter).toHaveBeenLastCalledWith({ kind: "header", key: "x-trace" });

    await user.click(screen.getByRole("button", { name: "Search by x-trace = t-1" }));
    expect(onFilter).toHaveBeenLastCalledWith({ kind: "header", key: "x-trace", value: "t-1" });

    await user.click(screen.getByRole("button", { name: "Search by x-empty = (empty)" }));
    expect(onFilter).toHaveBeenLastCalledWith({ kind: "header", key: "x-empty", value: "" });
  });

  it("shows each header as one line of text with no extra controls", () => {
    render(<HeaderLines headers={headers} masked={new Set()} onFilter={() => {}} />);

    const lines = screen.getAllByTestId("header-line");
    expect(lines.map((l) => l.textContent)).toEqual([
      "x-trace: t-1",
      "authorization: [redacted]",
      "x-empty: (empty)",
    ]);
    expect(within(lines[0]).getAllByRole("button")).toHaveLength(2);
  });

  it("leaves a masked value unclickable but keeps its key searchable", () => {
    render(
      <HeaderLines headers={headers} masked={new Set(["authorization"])} onFilter={() => {}} />,
    );

    const line = screen.getAllByTestId("header-line")[1];
    expect(within(line).getAllByRole("button")).toHaveLength(1);
    expect(within(line).getByRole("button")).toHaveAccessibleName("Search by header authorization");
    expect(within(line).getByText("masked")).toBeInTheDocument();
  });
});

describe("RecordKey", () => {
  it("searches by the key as the list renders it, not by its pretty-printed form", async () => {
    const onFilter = vi.fn();
    const user = userEvent.setup();
    render(<RecordKey display={'{\n  "id": 7\n}'} value={'{"id":7}'} onFilter={onFilter} />);

    await user.click(screen.getByRole("button", { name: 'Search by key {"id":7}' }));
    expect(onFilter).toHaveBeenCalledWith({ kind: "key", value: '{"id":7}' });
  });
});
