import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SearchQueryFields } from "./SearchQueryFields";
import { useSearchForm } from "./use-search-form";

const suggestions = { pathTree: new Map(), xmlPathTree: new Map(), sampleTooLargeToScan: false };
const headerKeys = { stats: [{ key: "x-trace", count: 2 }], total: 3 };

function Fields() {
  const form = useSearchForm("orders");
  return <SearchQueryFields form={form} suggestions={suggestions} headerKeys={headerKeys} />;
}

afterEach(cleanup);

describe("SearchQueryFields", () => {
  it("asks for a header key, an operator and a value in header mode", async () => {
    const user = userEvent.setup();
    render(<Fields />);

    await user.selectOptions(screen.getByLabelText("Mode"), "header");

    expect(screen.getByLabelText("Header key")).toBeInTheDocument();
    expect(screen.queryByLabelText("Path")).not.toBeInTheDocument();
    expect(screen.getByRole("option", { name: "missing" })).toBeInTheDocument();
    expect(screen.getByLabelText("Value")).toBeEnabled();
  });

  it("compares the record key without a path or header key", async () => {
    const user = userEvent.setup();
    render(<Fields />);

    await user.selectOptions(screen.getByLabelText("Mode"), "key");

    expect(screen.getByLabelText("Operator")).toBeInTheDocument();
    expect(screen.queryByLabelText("Path")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Header key")).not.toBeInTheDocument();
  });

  it("disables the value for presence checks and says why", async () => {
    const user = userEvent.setup();
    render(<Fields />);

    await user.selectOptions(screen.getByLabelText("Mode"), "header");
    await user.selectOptions(screen.getByLabelText("Operator"), "missing");

    const value = screen.getByLabelText("Value");
    expect(value).toBeDisabled();
    expect(value).toHaveAccessibleDescription("Not needed: missing checks presence only.");
  });
});
