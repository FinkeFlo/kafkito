import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { WhatsNewModal } from "./whats-new-modal";

describe("WhatsNewModal", () => {
  it("renders the title and the latest entry's items with type badges", () => {
    render(<WhatsNewModal currentVersion="0.0.0-rc17-btp" onClose={vi.fn()} />);
    expect(screen.getByText("What's new")).toBeInTheDocument();
    expect(
      screen.getByText(/create consumer groups bound to a topic/i),
    ).toBeInTheDocument();
    expect(screen.getByText("Security")).toBeInTheDocument();
  });
});
