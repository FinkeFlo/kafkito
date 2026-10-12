import { describe, expect, it } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { DiffView } from "./DiffView";

const lines = (n: number, change?: number) =>
  Array.from({ length: n }, (_, i) => (i === change ? "changed" : `line ${i}`)).join("\n");

describe("DiffView", () => {
  it("marks removed and added lines in words, not only colour", () => {
    render(<DiffView a={"a\nb\nc"} b={"a\nx\nc"} label="value" />);
    const table = screen.getByRole("table", {
      name: "Differences in the value, message A to message B",
    });
    expect(within(table).getByText("Removed")).toBeInTheDocument();
    expect(within(table).getByText("Added")).toBeInTheDocument();
  });

  it("says so when the view has no differences", () => {
    render(<DiffView a="same" b="same" label="key" />);
    expect(screen.getByText("No differences in this view.")).toBeVisible();
  });

  it("shows only the changes with no context", () => {
    render(<DiffView a={lines(20)} b={lines(20, 10)} label="value" context={0} />);
    expect(screen.queryByText("line 9")).toBeNull();
    expect(screen.getByText("changed")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Show 10 unchanged lines" })).toBeVisible();
    expect(screen.getByRole("button", { name: "Show 9 unchanged lines" })).toBeVisible();
  });

  it("reveals a long unchanged run in blocks", async () => {
    render(<DiffView a={lines(1300, 1299)} b={lines(1300)} label="value" />);
    const more = screen.getByRole("button", { name: "Show 500 of 1,296 unchanged lines" });
    await userEvent.click(more);
    expect(screen.getByText("line 0")).toBeInTheDocument();
    expect(screen.getByText("line 499")).toBeInTheDocument();
    expect(screen.queryByText("line 500")).toBeNull();
    expect(screen.getByRole("button", { name: "Show 500 of 796 unchanged lines" })).toBeVisible();
  });

  it("underlines the part of a changed line that differs", () => {
    render(<DiffView a={'"total": 12.5'} b={'"total": 15.5'} label="value" />);
    expect(screen.getByText("2", { selector: "span.underline" })).toBeInTheDocument();
    expect(screen.getByText("5", { selector: "span.underline" })).toBeInTheDocument();
  });
});
