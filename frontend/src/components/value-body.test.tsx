import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ValueBody } from "./value-body";
import { RawValueTooLargeError, type Message } from "@/lib/api";

const fetchMessageRawBase64 = vi.hoisted(() => vi.fn());

vi.mock("@/lib/api", async (importActual) => {
  const actual = await importActual<typeof import("@/lib/api")>();
  return { ...actual, fetchMessageRawBase64 };
});

const LOAD_BUTTON = /load full value to enable click-to-filter/i;

function message(overrides: Partial<Message> = {}): Message {
  return {
    partition: 0,
    offset: 42,
    timestamp_ms: 0,
    key_encoding: "text",
    value: '{"a":1',
    // The backend classifies a value cut off mid-structure by its first
    // non-whitespace byte, so truncated JSON keeps value_encoding: "json".
    value_encoding: "json",
    value_size_bytes: 128 * 1024,
    value_truncated: true,
    ...overrides,
  };
}

function renderValueBody(m: Message, onPick = vi.fn()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <ValueBody m={m} onPick={onPick} cluster="my-cluster" topic="my-topic" />
    </QueryClientProvider>,
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
        value_truncated: false,
        value_size_bytes: 17,
      }),
    );

    await user.click(screen.getByText('"A1"'));

    expect(onPick).toHaveBeenCalledWith([{ kind: "key", name: "orderId" }], "A1");
    expect(fetchMessageRawBase64).not.toHaveBeenCalled();
  });

  it("shows a load-full-value button instead of the tree for a truncated JSON value", () => {
    renderValueBody(message());

    expect(screen.getByRole("button", { name: LOAD_BUTTON })).toBeEnabled();
    // The truncated (and likely invalid) JSON is still shown as plain text.
    expect(screen.getByText('{"a":1')).toBeInTheDocument();
    expect(fetchMessageRawBase64).not.toHaveBeenCalled();
  });

  it("does not show the load-full-value button for truncated non-JSON text", () => {
    renderValueBody(
      message({
        value: "not json at all, just a long log line",
        value_encoding: "text",
      }),
    );

    expect(
      screen.queryByRole("button", { name: LOAD_BUTTON }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByText("not json at all, just a long log line"),
    ).toBeInTheDocument();
  });

  it("fetches the full value on click and renders the interactive tree", async () => {
    const base64 = Buffer.from(
      JSON.stringify({ orderId: "A1" }),
      "utf8",
    ).toString("base64");
    fetchMessageRawBase64.mockResolvedValue(base64);
    const user = userEvent.setup();
    const { onPick } = renderValueBody(message());

    await user.click(screen.getByRole("button", { name: LOAD_BUTTON }));

    await waitFor(() => expect(screen.getByText('"A1"')).toBeInTheDocument());
    expect(fetchMessageRawBase64).toHaveBeenCalledWith(
      "my-cluster",
      "my-topic",
      0,
      42,
      expect.anything(),
    );
    // The newly loaded content is announced to assistive technology.
    expect(screen.getByRole("status")).toHaveTextContent(/full value loaded/i);

    await user.click(screen.getByText('"A1"'));
    expect(onPick).toHaveBeenCalledWith([{ kind: "key", name: "orderId" }], "A1");
  });

  it("shows an alert and keeps the manual-entry hint when the full value exceeds the download limit", async () => {
    fetchMessageRawBase64.mockRejectedValue(
      new RawValueTooLargeError("too large"),
    );
    const user = userEvent.setup();
    renderValueBody(message());

    await user.click(screen.getByRole("button", { name: LOAD_BUTTON }));

    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent(
        /exceeds the download limit/i,
      ),
    );
    expect(screen.getByRole("alert")).toHaveTextContent(
      /enter the path manually instead/i,
    );
  });

  it("shows a parse error when the fetched full value is not valid JSON", async () => {
    fetchMessageRawBase64.mockResolvedValue(
      Buffer.from("not json", "utf8").toString("base64"),
    );
    const user = userEvent.setup();
    renderValueBody(message());

    await user.click(screen.getByRole("button", { name: LOAD_BUTTON }));

    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent(
        /could not be parsed as json/i,
      ),
    );
  });

  it("offers a retry after a failed fetch", async () => {
    fetchMessageRawBase64.mockRejectedValueOnce(new Error("network down"));
    fetchMessageRawBase64.mockResolvedValueOnce(
      Buffer.from(JSON.stringify({ orderId: "A1" }), "utf8").toString("base64"),
    );
    const user = userEvent.setup();
    renderValueBody(message());

    await user.click(screen.getByRole("button", { name: LOAD_BUTTON }));
    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent(/network down/i),
    );

    await user.click(screen.getByRole("button", { name: /retry/i }));
    await waitFor(() => expect(screen.getByText('"A1"')).toBeInTheDocument());
  });

  it("disables the button with a visible reason when the value exceeds the interactive limit", () => {
    renderValueBody(message({ value_size_bytes: 8 * 1024 * 1024 }));

    const button = screen.getByRole("button", { name: LOAD_BUTTON });
    expect(button).toBeDisabled();
    // The reason must be visible text, not only a title attribute, and it
    // must be wired to the button for screen readers.
    const describedBy = button.getAttribute("aria-describedby");
    expect(describedBy).toBeTruthy();
    const reason = document.getElementById(describedBy as string);
    expect(reason).toHaveTextContent(/above the .* interactive limit/i);
    expect(fetchMessageRawBase64).not.toHaveBeenCalled();
  });

  it("states the size and the limit in the same unit, so they can be compared", () => {
    renderValueBody(message({ value_size_bytes: 8 * 1024 * 1024 }));

    const button = screen.getByRole("button", { name: LOAD_BUTTON });
    const reason = document.getElementById(
      button.getAttribute("aria-describedby") as string,
    );

    // A decimal limit rendered by a binary formatter produced "977 KiB"
    // against a size in MiB, which reads as arbitrary and forces the reader
    // to convert units before the sentence means anything.
    const units = [...(reason?.textContent ?? "").matchAll(/\d\s*([KMG]iB)/g)].map(
      (m) => m[1],
    );
    expect(units.length).toBeGreaterThanOrEqual(2);
    expect(new Set(units).size).toBe(1);
  });

  it("does not offer the button for Schema Registry values, whose raw bytes are not JSON", () => {
    renderValueBody(
      message({
        value_sr: { format: "json", schema_id: 7 },
      } as Partial<Message>),
    );

    expect(
      screen.queryByRole("button", { name: LOAD_BUTTON }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByText(/not available for schema registry values/i),
    ).toBeInTheDocument();
    expect(fetchMessageRawBase64).not.toHaveBeenCalled();
  });
});
