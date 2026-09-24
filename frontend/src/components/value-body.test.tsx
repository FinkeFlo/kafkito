import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ValueBody } from "./value-body";
import { RawValueTooLargeError, type Message } from "@/lib/api";

const fetchMessageRawBase64 = vi.hoisted(() => vi.fn());

vi.mock("@/lib/api", async (importActual) => {
  const actual = await importActual<typeof import("@/lib/api")>();
  return { ...actual, fetchMessageRawBase64 };
});

function message(overrides: Partial<Message> = {}): Message {
  return {
    partition: 0,
    offset: 42,
    timestamp_ms: 0,
    key_encoding: "text",
    value: '{"a":1',
    // Truncation happens before the backend classifies the encoding, and a
    // value cut off mid-structure is (almost) never still valid JSON — so
    // real truncated JSON messages arrive as value_encoding: "text", not
    // "json". Mirrors the actual backend behavior (see looksLikeJson).
    value_encoding: "text",
    value_size_bytes: 8 * 1024 * 1024,
    value_truncated: true,
    ...overrides,
  };
}

function renderValueBody(m: Message, onPick = vi.fn()) {
  render(
    <ValueBody m={m} onPick={onPick} cluster="my-cluster" topic="my-topic" />,
  );
  return { onPick };
}

describe("ValueBody", () => {
  beforeEach(() => {
    fetchMessageRawBase64.mockReset();
  });

  it("renders the interactive tree directly for a non-truncated JSON value", async () => {
    const user = userEvent.setup();
    const { onPick } = renderValueBody(
      message({
        value: '{"orderId":"A1"}',
        // A complete (non-truncated) JSON value is correctly classified by
        // the backend, unlike the truncated case the other tests exercise.
        value_encoding: "json",
        value_truncated: false,
        value_size_bytes: 17,
      }),
    );

    await user.click(screen.getByText('"A1"'));

    expect(onPick).toHaveBeenCalledWith(
      [{ kind: "key", name: "orderId" }],
      "A1",
      [0],
    );
    expect(fetchMessageRawBase64).not.toHaveBeenCalled();
  });

  it("shows a load-full-value button instead of the tree for a truncated, JSON-looking value (value_encoding: text, as the backend reports it once truncated)", () => {
    renderValueBody(message());

    expect(
      screen.getByRole("button", {
        name: /load full value to enable click-to-filter/i,
      }),
    ).toBeInTheDocument();
    // The truncated (and likely invalid) JSON is still shown as plain text.
    expect(screen.getByText('{"a":1')).toBeInTheDocument();
    expect(fetchMessageRawBase64).not.toHaveBeenCalled();
  });

  it("does not show the load-full-value button for truncated text that doesn't look like JSON", () => {
    renderValueBody(
      message({ value: "not json at all, just a long log line" }),
    );

    expect(
      screen.queryByRole("button", {
        name: /load full value to enable click-to-filter/i,
      }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByText("not json at all, just a long log line"),
    ).toBeInTheDocument();
  });

  it("fetches the full value on click and renders the interactive tree", async () => {
    const fullValue = JSON.stringify({ orderId: "A1" });
    const base64 = Buffer.from(fullValue, "utf8").toString("base64");
    fetchMessageRawBase64.mockResolvedValue(base64);
    const user = userEvent.setup();
    const { onPick } = renderValueBody(message());

    await user.click(
      screen.getByRole("button", {
        name: /load full value to enable click-to-filter/i,
      }),
    );

    await waitFor(() => expect(screen.getByText('"A1"')).toBeInTheDocument());
    expect(fetchMessageRawBase64).toHaveBeenCalledWith(
      "my-cluster",
      "my-topic",
      0,
      42,
    );

    await user.click(screen.getByText('"A1"'));
    expect(onPick).toHaveBeenCalledWith(
      [{ kind: "key", name: "orderId" }],
      "A1",
      [0],
    );
  });

  it("shows a clear error and keeps the manual-entry hint when the full value exceeds the download limit", async () => {
    fetchMessageRawBase64.mockRejectedValue(
      new RawValueTooLargeError("too large"),
    );
    const user = userEvent.setup();
    renderValueBody(message());

    await user.click(
      screen.getByRole("button", {
        name: /load full value to enable click-to-filter/i,
      }),
    );

    await waitFor(() =>
      expect(
        screen.getByText(/exceeds the download limit/i),
      ).toBeInTheDocument(),
    );
    expect(screen.getByText(/enter the path manually instead/i)).toBeInTheDocument();
  });

  it("shows a parse error when the fetched full value is not valid JSON", async () => {
    fetchMessageRawBase64.mockResolvedValue(
      Buffer.from("not json", "utf8").toString("base64"),
    );
    const user = userEvent.setup();
    renderValueBody(message());

    await user.click(
      screen.getByRole("button", {
        name: /load full value to enable click-to-filter/i,
      }),
    );

    await waitFor(() =>
      expect(
        screen.getByText(/could not be parsed as json/i),
      ).toBeInTheDocument(),
    );
  });
});
