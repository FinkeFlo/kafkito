import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ReplayModal } from "./replay-modal";
import { RawValueTooLargeError, type Message } from "@/lib/api";
import type { ClusterListItem } from "@/lib/use-cluster";

const fetchMessageRawBase64 = vi.hoisted(() => vi.fn());
const fetchTopics = vi.hoisted(() => vi.fn());
const produceMessage = vi.hoisted(() => vi.fn());
const useCluster = vi.hoisted(() => vi.fn());

vi.mock("@/lib/api", async (importActual) => {
  const actual = await importActual<typeof import("@/lib/api")>();
  return { ...actual, fetchMessageRawBase64, fetchTopics, produceMessage };
});

// useCluster pulls in the router; the modal only needs the cluster list.
vi.mock("@/lib/use-cluster", () => ({ useCluster }));

function cluster(name: string): ClusterListItem {
  return {
    source: "shared",
    name,
    reachable: true,
    is_prod: false,
    auth_type: "none",
    tls: false,
    schema_registry: false,
  };
}

function truncatedMessage(overrides: Partial<Message> = {}): Message {
  return {
    partition: 0,
    offset: 42,
    timestamp_ms: 0,
    key_encoding: "text",
    value: "x".repeat(1000),
    value_encoding: "text",
    value_size_bytes: 8 * 1024 * 1024,
    value_truncated: true,
    ...overrides,
  };
}

function renderModal(message: Message, props: Partial<React.ComponentProps<typeof ReplayModal>> = {}) {
  useCluster.mockReturnValue({
    clusters: [cluster("dest-a")],
  });
  fetchTopics.mockResolvedValue([]);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <ReplayModal
        open
        onClose={() => {}}
        message={message}
        sourceCluster="src"
        sourceTopic="src-topic"
        {...props}
      />
    </QueryClientProvider>,
  );
}

describe("ReplayModal truncated value handling", () => {
  beforeEach(() => {
    fetchMessageRawBase64.mockReset();
    fetchTopics.mockReset();
    produceMessage.mockReset();
  });

  it("auto-fetches and uses the full value, enabling Replay without any opt-in", async () => {
    fetchMessageRawBase64.mockResolvedValue("ZnVsbC12YWx1ZQ==");
    const user = userEvent.setup();
    renderModal(truncatedMessage());

    await waitFor(() =>
      expect(screen.getByText(/full value .* loaded/i)).toBeInTheDocument(),
    );
    await user.type(screen.getByPlaceholderText("topic-name"), "dest-topic");
    expect(screen.getByRole("button", { name: /^replay$/i })).toBeEnabled();
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
  });

  it("blocks Replay and requires explicit opt-in when the full value cannot be recovered", async () => {
    fetchMessageRawBase64.mockRejectedValue(new RawValueTooLargeError("too large"));
    const user = userEvent.setup();
    renderModal(truncatedMessage());

    await waitFor(() =>
      expect(screen.getByText(/only a 64.?kb preview is available/i)).toBeInTheDocument(),
    );
    await user.type(screen.getByPlaceholderText("topic-name"), "dest-topic");
    expect(screen.getByRole("button", { name: /^replay$/i })).toBeDisabled();

    await user.click(screen.getByRole("checkbox", { name: /replay the truncated/i }));

    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^replay$/i })).toBeEnabled(),
    );
  });

  it("without sourceCluster/sourceTopic, falls straight to the opt-in-required state", async () => {
    renderModal(truncatedMessage(), { sourceCluster: undefined, sourceTopic: undefined });

    await waitFor(() =>
      expect(screen.getByText(/only a 64.?kb preview is available/i)).toBeInTheDocument(),
    );
    expect(fetchMessageRawBase64).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: /^replay$/i })).toBeDisabled();
  });

  it("does not auto-fetch when the message is already blocked (e.g. masked)", () => {
    renderModal(truncatedMessage({ masked: true }));

    expect(screen.getByText(/masked message/i)).toBeInTheDocument();
    expect(fetchMessageRawBase64).not.toHaveBeenCalled();
  });

  it("does not gate Replay on non-truncated messages", () => {
    renderModal({
      partition: 0,
      offset: 1,
      timestamp_ms: 0,
      key_encoding: "text",
      value: "small",
      value_encoding: "text",
    });

    expect(fetchMessageRawBase64).not.toHaveBeenCalled();
    expect(screen.queryByText(/64.?kb preview/i)).not.toBeInTheDocument();
  });
});
