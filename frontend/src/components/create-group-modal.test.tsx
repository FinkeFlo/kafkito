import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { CreateGroupModal } from "./create-group-modal";

const createGroup = vi.hoisted(() => vi.fn());

vi.mock("@/lib/api", async (importActual) => {
  const actual = await importActual<typeof import("@/lib/api")>();
  return { ...actual, createGroup };
});

function renderModal(onCreated?: () => void, onClose: () => void = () => {}) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const invalidateQueries = vi.spyOn(qc, "invalidateQueries");
  return {
    qc,
    invalidateQueries,
    ...render(
      <QueryClientProvider client={qc}>
        <CreateGroupModal
          cluster="C"
          topic="t1"
          onClose={onClose}
          onCreated={onCreated}
        />
      </QueryClientProvider>,
    ),
  };
}

describe("CreateGroupModal", () => {
  beforeEach(() => {
    createGroup.mockReset();
  });

  it("renders with strategy defaulting to latest", () => {
    renderModal();

    expect(
      screen.getByRole("combobox", { name: /strategy/i }),
    ).toHaveValue("latest");
  });

  it("previews with dry_run true and shows the returned per-partition new offset", async () => {
    const user = userEvent.setup();
    createGroup.mockResolvedValueOnce({
      group: "g1",
      topic: "t1",
      dry_run: true,
      results: [
        { partition: 0, old_offset: -1, new_offset: 42, end_offset: 42 },
      ],
    });
    renderModal();

    await user.type(screen.getByLabelText(/group name/i), "g1");
    await user.click(screen.getByRole("button", { name: /preview/i }));

    await waitFor(() =>
      expect(createGroup).toHaveBeenCalledWith("C", {
        group_id: "g1",
        topic: "t1",
        strategy: "latest",
        offset: undefined,
        timestamp_ms: undefined,
        dry_run: true,
      }),
    );
    expect(await screen.findByText("42")).toBeInTheDocument();
  });

  it("clicking Create calls createGroup with dry_run false, invalidates the topic-consumers query, then calls onCreated and onClose", async () => {
    const user = userEvent.setup();
    const onCreated = vi.fn();
    const onClose = vi.fn();
    createGroup.mockResolvedValueOnce({
      group: "g1",
      topic: "t1",
      dry_run: false,
      results: [
        { partition: 0, old_offset: -1, new_offset: 42, end_offset: 42 },
      ],
    });
    const { invalidateQueries } = renderModal(onCreated, onClose);

    await user.type(screen.getByLabelText(/group name/i), "g1");
    await user.click(screen.getByRole("button", { name: /^create$/i }));

    await waitFor(() =>
      expect(createGroup).toHaveBeenCalledWith("C", {
        group_id: "g1",
        topic: "t1",
        strategy: "latest",
        offset: undefined,
        timestamp_ms: undefined,
        dry_run: false,
      }),
    );
    await waitFor(() =>
      expect(invalidateQueries).toHaveBeenCalledWith({
        queryKey: ["topic-consumers", "C", "t1"],
      }),
    );
    await waitFor(() => expect(onCreated).toHaveBeenCalled());
    await waitFor(() => expect(onClose).toHaveBeenCalled());

    // onCreated must fire before onClose (Create → invalidate → onCreated → onClose).
    const createdOrder = onCreated.mock.invocationCallOrder[0];
    const closeOrder = onClose.mock.invocationCallOrder[0];
    expect(createdOrder).toBeLessThan(closeOrder);
  });

  it("round-trips the timestamp strategy through the local-time picker without a UTC-offset shift", async () => {
    const user = userEvent.setup();
    createGroup.mockResolvedValueOnce({
      group: "g1",
      topic: "t1",
      dry_run: true,
      results: [],
    });
    const { container } = renderModal();

    await user.type(screen.getByLabelText(/group name/i), "g1");
    await user.selectOptions(
      screen.getByRole("combobox", { name: /strategy/i }),
      "timestamp",
    );

    const picker = container.querySelector<HTMLInputElement>(
      'input[type="datetime-local"]',
    )!;
    await user.clear(picker);
    await user.type(picker, "2026-06-15T14:45:00");

    await user.click(screen.getByRole("button", { name: /preview/i }));

    await waitFor(() =>
      expect(createGroup).toHaveBeenCalledWith(
        "C",
        expect.objectContaining({ strategy: "timestamp" }),
      ),
    );
    const body = createGroup.mock.calls.at(-1)![1];
    expect(typeof body.timestamp_ms).toBe("number");
    // Interpreting the picker value as local time (not UTC) is what guards
    // against a shift by the local UTC offset.
    expect(body.timestamp_ms).toBe(new Date("2026-06-15T14:45:00").getTime());
  });

  it("disables Preview/Create for a non-integer offset under the offset strategy", async () => {
    const user = userEvent.setup();
    renderModal();

    await user.type(screen.getByLabelText(/group name/i), "g1");
    await user.selectOptions(
      screen.getByRole("combobox", { name: /strategy/i }),
      "offset",
    );

    const offsetInput = screen.getByLabelText(/offset/i);
    await user.clear(offsetInput);
    await user.type(offsetInput, "1.5");

    expect(screen.getByRole("button", { name: /preview/i })).toBeDisabled();
    expect(screen.getByRole("button", { name: /^create$/i })).toBeDisabled();

    // A valid integer re-enables the actions.
    await user.clear(offsetInput);
    await user.type(offsetInput, "10");

    expect(screen.getByRole("button", { name: /preview/i })).toBeEnabled();
    expect(screen.getByRole("button", { name: /^create$/i })).toBeEnabled();
  });

  it("surfaces an error notice when the group already exists", async () => {
    const user = userEvent.setup();
    createGroup.mockRejectedValueOnce(new Error("HTTP 409: group already exists"));
    renderModal();

    await user.type(screen.getByLabelText(/group name/i), "g1");
    await user.click(screen.getByRole("button", { name: /^create$/i }));

    expect(await screen.findByText(/already exists/i)).toBeInTheDocument();
  });
});
