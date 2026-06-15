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
    resetGroupOffsets.mockResolvedValue({
      group: "g1",
      topic: "t1",
      dry_run: true,
      results: [],
    });
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

  it("selects every partition by default and enables Commit reset", () => {
    renderModal([0, 1, 2]);

    for (const p of [0, 1, 2]) {
      expect(
        screen.getByRole("checkbox", { name: new RegExp(`^p${p}$`) }),
      ).toBeChecked();
    }
    expect(screen.getByText(/3 of 3 selected/i)).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /commit reset/i }),
    ).toBeEnabled();
  });
});

describe("ResetOffsetsModal timestamp strategy", () => {
  beforeEach(() => {
    resetGroupOffsets.mockReset();
    resetGroupOffsets.mockResolvedValue({
      group: "g1",
      topic: "t1",
      dry_run: true,
      results: [],
    });
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
    // The picker resolves to a human-readable UTC instant; the raw epoch ms
    // (internal wire format) is intentionally not surfaced to users.
    expect(screen.getByText(/resolves to/i)).toBeInTheDocument();
    expect(screen.queryByText(/epoch ms/i)).not.toBeInTheDocument();
  });

  it("auto-previews with a numeric timestamp_ms derived from the picker value", async () => {
    const user = userEvent.setup();
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

    // No Preview button: the dry-run fires automatically (debounced).
    await waitFor(
      () =>
        expect(resetGroupOffsets.mock.calls.at(-1)?.[2]?.strategy).toBe(
          "timestamp",
        ),
      { timeout: 2000 },
    );
    const body = resetGroupOffsets.mock.calls.at(-1)![2];
    expect(typeof body.timestamp_ms).toBe("number");
    expect(body.timestamp_ms).toBe(new Date("2026-06-15T14:45:00").getTime());
    expect(body.dry_run).toBe(true);
  });

  it("auto-previews the approximate consumer lag (end - new) for all partitions by default and as a total", async () => {
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

    // All partitions are selected by default, so the projected new lag shows
    // immediately without toggling anything.
    // p0 lag = 100 - 80 = 20, p1 lag = 100 - 30 = 70, total = 90.
    await waitFor(() =>
      expect(
        screen.getByText(/total group lag after reset/i),
      ).toBeInTheDocument(),
    );
    expect(screen.getByText("20")).toBeInTheDocument();
    expect(screen.getByText("70")).toBeInTheDocument();
    expect(screen.getByText("90")).toBeInTheDocument();
  });
});
