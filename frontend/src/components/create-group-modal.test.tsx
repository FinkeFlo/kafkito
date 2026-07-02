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

function renderModal(onCreated?: () => void) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return {
    qc,
    ...render(
      <QueryClientProvider client={qc}>
        <CreateGroupModal
          cluster="C"
          topic="t1"
          onClose={() => {}}
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

  it("confirming create calls createGroup with dry_run false", async () => {
    const user = userEvent.setup();
    const onCreated = vi.fn();
    createGroup.mockResolvedValueOnce({
      group: "g1",
      topic: "t1",
      dry_run: false,
      results: [
        { partition: 0, old_offset: -1, new_offset: 42, end_offset: 42 },
      ],
    });
    renderModal(onCreated);

    await user.type(screen.getByLabelText(/group name/i), "g1");
    await user.click(screen.getByRole("button", { name: /^create$/i }));
    await user.click(
      screen.getByRole("button", { name: /create consumer group/i }),
    );

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
    await waitFor(() => expect(onCreated).toHaveBeenCalled());
  });

  it("surfaces an error notice when the group already exists", async () => {
    const user = userEvent.setup();
    createGroup.mockRejectedValueOnce(new Error("HTTP 409: group already exists"));
    renderModal();

    await user.type(screen.getByLabelText(/group name/i), "g1");
    await user.click(screen.getByRole("button", { name: /^create$/i }));
    await user.click(
      screen.getByRole("button", { name: /create consumer group/i }),
    );

    expect(await screen.findByText(/already exists/i)).toBeInTheDocument();
  });
});
