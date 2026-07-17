import type { ComponentProps } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  MESSAGE_RANGE_COUNT_DEBOUNCE_MS,
  MessageRangeCountPreview,
} from "./message-range-count-preview";

const fetchMessageCount = vi.hoisted(() => vi.fn());

vi.mock("@/lib/api", async (importActual) => {
  const actual = await importActual<typeof import("@/lib/api")>();
  return { ...actual, fetchMessageCount };
});

function renderPreview(
  props?: Partial<ComponentProps<typeof MessageRangeCountPreview>>,
) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const view = render(
    <QueryClientProvider client={qc}>
      <MessageRangeCountPreview
        cluster="c1"
        topic="orders"
        partition={-1}
        from_ts_ms={100}
        to_ts_ms={200}
        live={false}
        {...props}
      />
    </QueryClientProvider>,
  );
  return { ...view, qc };
}

async function sleep(ms: number) {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, ms));
  });
}

describe("MessageRangeCountPreview", () => {
  beforeEach(() => {
    window.localStorage.setItem("kafkito.numberFormat", "en");
    fetchMessageCount.mockReset();
  });

  it("debounces the query and shows an expandable partition breakdown", async () => {
    const user = userEvent.setup();
    fetchMessageCount.mockResolvedValue({
      cluster: "c1",
      topic: "orders",
      from_ts_ms: 100,
      to_ts_ms: 200,
      total_approx_count: 128450,
      partitions: [
        { partition: 0, from_offset: 1000, to_offset: 65225, approx_count: 64225 },
        { partition: 1, from_offset: 500, to_offset: 64725, approx_count: 64225 },
      ],
    });

    renderPreview();

    await waitFor(() =>
      expect(fetchMessageCount).toHaveBeenCalledWith("c1", "orders", {
        partition: -1,
        from_ts_ms: 100,
        to_ts_ms: 200,
      }),
    );

    expect(await screen.findByText("≈ 128,450 messages")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /per partition/i }));

    expect(screen.getByText("p0")).toBeInTheDocument();
    expect(screen.getAllByText("64,225")).toHaveLength(2);
    expect(screen.getByText("1,000")).toBeInTheDocument();
  });

  it("keeps the previous total visible while a new range is loading", async () => {
    let resolveSecond: ((value: unknown) => void) | null = null;
    fetchMessageCount
      .mockResolvedValueOnce({
        cluster: "c1",
        topic: "orders",
        total_approx_count: 150,
        partitions: [{ partition: 0, from_offset: 0, to_offset: 150, approx_count: 150 }],
      })
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveSecond = resolve;
          }),
      );

    const { rerender, qc } = renderPreview();

    expect(await screen.findByText("≈ 150 messages")).toBeInTheDocument();

    rerender(
      <QueryClientProvider client={qc}>
        <MessageRangeCountPreview
          cluster="c1"
          topic="orders"
          partition={-1}
          from_ts_ms={200}
          to_ts_ms={300}
          live={false}
        />
      </QueryClientProvider>,
    );

    expect(fetchMessageCount).toHaveBeenCalledTimes(1);
    await sleep(MESSAGE_RANGE_COUNT_DEBOUNCE_MS - 150);
    expect(fetchMessageCount).toHaveBeenCalledTimes(1);

    await sleep(200);
    await waitFor(() => expect(fetchMessageCount).toHaveBeenCalledTimes(2));

    expect(screen.getByText("≈ 150 messages")).toBeInTheDocument();
    expect(screen.getByLabelText(/updating/i)).toBeInTheDocument();

    await act(async () => {
      resolveSecond?.({
        cluster: "c1",
        topic: "orders",
        total_approx_count: 275,
        partitions: [{ partition: 0, from_offset: 10, to_offset: 285, approx_count: 275 }],
      });
    });

    await waitFor(() =>
      expect(screen.getByText("≈ 275 messages")).toBeInTheDocument(),
    );
  });

  it("shows a paused snapshot state while live mode is enabled", () => {
    renderPreview({ live: true });

    expect(fetchMessageCount).not.toHaveBeenCalled();
    expect(screen.getByText(/snapshot paused/i)).toBeInTheDocument();
  });

  it("honors the DE number format preference", async () => {
    window.localStorage.setItem("kafkito.numberFormat", "de");
    fetchMessageCount.mockResolvedValue({
      cluster: "c1",
      topic: "orders",
      total_approx_count: 3504776,
      partitions: [
        { partition: 0, from_offset: 1000, to_offset: 3505776, approx_count: 3504776 },
      ],
    });

    renderPreview();

    expect(await screen.findByText("≈ 3.504.776 messages")).toBeInTheDocument();
  });
});
