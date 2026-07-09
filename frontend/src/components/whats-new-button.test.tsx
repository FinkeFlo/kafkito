import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { WhatsNewButton } from "./whats-new-button";

const fetchInfo = vi.hoisted(() => vi.fn());
vi.mock("@/lib/api", async (imp) => ({
  ...(await imp<typeof import("@/lib/api")>()),
  fetchInfo,
}));

function renderButton() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <WhatsNewButton />
    </QueryClientProvider>,
  );
}

describe("WhatsNewButton", () => {
  beforeEach(() => {
    window.localStorage.clear();
    fetchInfo.mockReset();
    fetchInfo.mockResolvedValue({ name: "kafkito", version: "0.0.0-rc17-btp" });
  });

  it("auto-opens once for an unseen version and marks it seen", async () => {
    renderButton();
    expect(await screen.findByText("What's new")).toBeInTheDocument();
    await waitFor(() =>
      expect(window.localStorage.getItem("kafkito.whatsnew.lastSeen.v1")).toBe(
        "0.0.0-rc17",
      ),
    );
  });

  it("does not auto-open and shows no dot once the version is seen", async () => {
    window.localStorage.setItem("kafkito.whatsnew.lastSeen.v1", "0.0.0-rc17");
    renderButton();
    await screen.findByRole("button", { name: /what's new/i });
    expect(screen.queryByText("What's new")).not.toBeInTheDocument();
  });

  it("opens on click", async () => {
    window.localStorage.setItem("kafkito.whatsnew.lastSeen.v1", "0.0.0-rc17");
    const user = userEvent.setup();
    renderButton();
    await user.click(screen.getByRole("button", { name: /what's new/i }));
    expect(await screen.findByText("What's new")).toBeInTheDocument();
  });
});
