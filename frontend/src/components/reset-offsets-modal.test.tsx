import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ResetOffsetsModal } from "./reset-offsets-modal";
import type { GroupDetail } from "@/lib/api";

function detailWith(parts: number[]): GroupDetail {
  return {
    group_id: "g1",
    state: "Empty",
    protocol_type: "consumer",
    coordinator_id: 1,
    members: [],
    topics: 1,
    lag: 0,
    lag_known: true,
    offsets: parts.map((p) => ({
      topic: "t1",
      partition: p,
      offset: 0,
      log_end: 100,
      lag: 100,
    })),
  };
}

function renderModal(parts: number[]) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <ResetOffsetsModal cluster="C" detail={detailWith(parts)} onClose={() => {}} />
    </QueryClientProvider>,
  );
}

describe("ResetOffsetsModal partition picker a11y", () => {
  it.each([0, 1, 2])(
    "renders partition %i checkbox with a discoverable accessible name",
    (p) => {
      renderModal([0, 1, 2]);

      const checkbox = screen.getByRole("checkbox", {
        name: new RegExp(`^p${p}$`),
      });

      expect(checkbox).toBeInTheDocument();
      // The id+htmlFor binding is the WCAG 4.1.2 fix: a label must reference
      // the input via htmlFor for assistive tech to compute the accessible name.
      expect(checkbox.id).toBe(`reset-offsets-partition-${p}`);
      // The checkbox must remain in the a11y tree. Tailwind's `hidden`
      // (display:none) removes it; `sr-only` keeps it visible to AT.
      expect(checkbox).not.toHaveClass("hidden");
    },
  );
});
