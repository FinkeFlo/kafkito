import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ResetOffsetsModal } from "./reset-offsets-modal";
import type { GroupDetail } from "@/lib/api";

const resetGroupOffsets = vi.hoisted(() => vi.fn());

vi.mock("@/lib/api", async (importActual) => {
  const actual = await importActual<typeof import("@/lib/api")>();
  return { ...actual, resetGroupOffsets };
});

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
  beforeEach(() => {
    resetGroupOffsets.mockReset();
  });
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

describe("ResetOffsetsModal timestamp strategy", () => {
  beforeEach(() => {
    resetGroupOffsets.mockReset();
  });

  it("renders a date & time picker with relative quick buttons instead of a raw epoch field", async () => {
    const user = userEvent.setup();
    const { container } = renderModal([0]);

    await user.selectOptions(
      screen.getByRole("combobox", { name: /strategy/i }),
      "timestamp",
    );

    const picker = container.querySelector<HTMLInputElement>(
      'input[type="datetime-local"]',
    );
    expect(picker).not.toBeNull();
    expect(picker!.value).not.toBe("");
    expect(screen.getByRole("button", { name: "-1h" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "-6h" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "-24h" })).toBeInTheDocument();
    // Power-user affordance: the resolved epoch ms is still surfaced.
    expect(screen.getByText(/epoch ms/i)).toBeInTheDocument();
  });

  it("sends a numeric timestamp_ms derived from the picker value", async () => {
    const user = userEvent.setup();
    resetGroupOffsets.mockResolvedValue({
      group: "g1",
      topic: "t1",
      dry_run: true,
      results: [],
    });
    const { container } = renderModal([0]);

    await user.selectOptions(
      screen.getByRole("combobox", { name: /strategy/i }),
      "timestamp",
    );
    const picker = container.querySelector<HTMLInputElement>(
      'input[type="datetime-local"]',
    )!;
    await user.clear(picker);
    await user.type(picker, "2026-06-15T14:45:00");

    await user.click(screen.getByRole("button", { name: /^preview$/i }));

    await waitFor(() => expect(resetGroupOffsets).toHaveBeenCalled());
    const body = resetGroupOffsets.mock.calls.at(-1)![2];
    expect(body.strategy).toBe("timestamp");
    expect(typeof body.timestamp_ms).toBe("number");
    expect(body.timestamp_ms).toBe(new Date("2026-06-15T14:45:00").getTime());
  });

  it("previews the approximate consumer lag (end - new) per partition and as a total", async () => {
    const user = userEvent.setup();
    resetGroupOffsets.mockResolvedValue({
      group: "g1",
      topic: "t1",
      dry_run: true,
      results: [
        { partition: 0, old_offset: 50, new_offset: 80, end_offset: 100 },
        { partition: 1, old_offset: 10, new_offset: 30, end_offset: 100 },
      ],
    });
    renderModal([0, 1]);

    await user.click(screen.getByRole("button", { name: /^preview$/i }));

    // p0 lag = 100 - 80 = 20, p1 lag = 100 - 30 = 70, total = 90.
    await waitFor(() =>
      expect(screen.getByText(/total lag after reset/i)).toBeInTheDocument(),
    );
    expect(screen.getByText("20")).toBeInTheDocument();
    expect(screen.getByText("70")).toBeInTheDocument();
    expect(screen.getByText("90")).toBeInTheDocument();
  });
});
