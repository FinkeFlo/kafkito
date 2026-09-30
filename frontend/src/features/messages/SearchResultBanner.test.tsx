import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import type { SearchStats } from "@/lib/api";
import { SearchResultBanner } from "./SearchResultBanner";
import type { SearchStopReason } from "./search-chain";

function renderBanner(stopReason: SearchStopReason, over: Partial<SearchStats>) {
  const stats: SearchStats = {
    scanned: 10,
    matched: 1,
    budget_exhausted: false,
    timed_out: false,
    more_available: false,
    direction: "newest_first",
    parse_errors: 0,
    ...over,
  };
  render(
    <SearchResultBanner
      result={{ messages: [], stats, req: { partition: -1 } }}
      searching={false}
      stopReason={stopReason}
      onSearchMore={() => {}}
    />,
  );
}

describe("SearchResultBanner", () => {
  it("shows a complete range as fully scanned", () => {
    renderBanner("complete", {});
    expect(screen.getByText("Range fully scanned")).toBeInTheDocument();
  });

  it("never shows a timed-out search as fully scanned", () => {
    renderBanner("complete", { timed_out: true });
    expect(screen.queryByText("Range fully scanned")).not.toBeInTheDocument();
  });

  it("shows a chain stopped without progress as timed out and offers to search more", () => {
    renderBanner("timeout", { timed_out: true, more_available: true });
    expect(screen.getByText("Timed out")).toBeInTheDocument();
    expect(screen.queryByText("Range fully scanned")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Search more/ })).toBeInTheDocument();
  });
});
