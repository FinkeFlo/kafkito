import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { topicQueries } from "@/lib/queries/topics";
import { CreateTopicModal } from "./CreateTopicModal";

const createTopic = vi.hoisted(() => vi.fn());
const toastSuccess = vi.hoisted(() => vi.fn());

vi.mock("@/lib/api", async (importActual) => {
  const actual = await importActual<typeof import("@/lib/api")>();
  return { ...actual, createTopic };
});
vi.mock("sonner", () => ({ toast: { success: toastSuccess } }));

function renderModal({
  brokers,
  onClose = vi.fn(),
}: {
  brokers?: number;
  onClose?: () => void;
} = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const invalidateQueries = vi.spyOn(qc, "invalidateQueries");
  render(
    <QueryClientProvider client={qc}>
      <CreateTopicModal cluster="C" brokers={brokers} onClose={onClose} />
    </QueryClientProvider>,
  );
  return { invalidateQueries, onClose };
}

const createButton = () => screen.getByRole("button", { name: /^create$/i });

describe("CreateTopicModal", () => {
  beforeEach(() => {
    createTopic.mockReset();
    toastSuccess.mockReset();
    createTopic.mockResolvedValue({ created: "orders.v2" });
  });

  it("keeps Create disabled until a name is entered", async () => {
    renderModal();
    expect(createButton()).toBeDisabled();
    await userEvent.type(screen.getByLabelText("Name"), "orders.v2");
    expect(createButton()).toBeEnabled();
  });

  it("shows an inline error for an illegal name and blocks submit", async () => {
    renderModal();
    const name = screen.getByLabelText("Name");
    await userEvent.type(name, "orders v2");
    expect(name).toHaveAttribute("aria-invalid", "true");
    expect(name).toHaveAccessibleDescription(/only letters, digits/i);
    expect(createButton()).toBeDisabled();
  });

  it("submits with Enter and sends numbers and configs", async () => {
    const { invalidateQueries, onClose } = renderModal();
    await userEvent.type(screen.getByLabelText("Name"), "orders.v2");
    const partitions = screen.getByLabelText("Partitions");
    await userEvent.clear(partitions);
    await userEvent.type(partitions, "6");
    await userEvent.click(screen.getByRole("button", { name: /add config/i }));
    await userEvent.type(screen.getByLabelText("Config 1 key"), "cleanup.policy");
    await userEvent.type(screen.getByLabelText("Config 1 value"), "compact");
    await userEvent.type(screen.getByLabelText("Name"), "{Enter}");

    await waitFor(() => expect(createTopic).toHaveBeenCalledTimes(1));
    expect(createTopic).toHaveBeenCalledWith("C", {
      name: "orders.v2",
      partitions: 6,
      replication_factor: 1,
      configs: { "cleanup.policy": "compact" },
    });
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(toastSuccess).toHaveBeenCalledWith('Topic "orders.v2" created');
    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: topicQueries.list("C").queryKey });
  });

  it("lets a number field be cleared and retyped instead of snapping to 1", async () => {
    renderModal();
    await userEvent.type(screen.getByLabelText("Name"), "t");
    const partitions = screen.getByLabelText("Partitions");
    await userEvent.clear(partitions);
    expect(partitions).toHaveValue("");
    expect(partitions).toHaveAccessibleDescription(/whole number of at least 1/i);
    expect(createButton()).toBeDisabled();
    await userEvent.type(partitions, "12");
    expect(partitions).toHaveValue("12");
    expect(createButton()).toBeEnabled();
  });

  it("rejects a replication factor above the broker count when it is known", async () => {
    renderModal({ brokers: 3 });
    await userEvent.type(screen.getByLabelText("Name"), "t");
    const rf = screen.getByLabelText("Replication factor");
    await userEvent.clear(rf);
    await userEvent.type(rf, "4");
    expect(rf).toHaveAccessibleDescription(/cluster has 3 brokers/i);
    expect(rf).toHaveAttribute("aria-invalid", "true");
    expect(createButton()).toBeDisabled();
  });

  it("summarises the replica count in the footer", async () => {
    renderModal();
    const partitions = screen.getByLabelText("Partitions");
    await userEvent.clear(partitions);
    await userEvent.type(partitions, "6");
    const rf = screen.getByLabelText("Replication factor");
    await userEvent.clear(rf);
    await userEvent.type(rf, "3");
    expect(screen.getByText(/= 18 replicas/)).toBeInTheDocument();
  });

  it("removes the chosen config row and keeps the others intact", async () => {
    renderModal();
    const add = screen.getByRole("button", { name: /add config/i });
    await userEvent.click(add);
    await userEvent.click(add);
    await userEvent.type(screen.getByLabelText("Config 1 key"), "first");
    await userEvent.type(screen.getByLabelText("Config 2 key"), "second");
    await userEvent.click(screen.getByRole("button", { name: "Remove config first" }));
    expect(screen.getByLabelText("Config 1 key")).toHaveValue("second");
    expect(screen.queryByLabelText("Config 2 key")).toBeNull();
  });

  it("keeps the dialog open and shows the server error when creation fails", async () => {
    createTopic.mockRejectedValueOnce(new Error("TOPIC_ALREADY_EXISTS"));
    const { onClose } = renderModal();
    await userEvent.type(screen.getByLabelText("Name"), "orders.v2{Enter}");
    expect(await screen.findByText("TOPIC_ALREADY_EXISTS")).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
  });
});
