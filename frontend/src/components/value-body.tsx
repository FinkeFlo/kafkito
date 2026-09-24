// ValueBody — renders a message's value in the messages list. For JSON
// values it shows the interactive, click-to-filter tree (JsonInteractive);
// otherwise it falls back to a pretty-printed <pre> block.
//
// Large values are a special case: the message list only ever holds the
// first 64 KB of a value (`message.value_truncated`), and a truncated JSON
// preview is usually cut mid-structure, so JSON.parse throws and silently
// falls back to plain text — disabling click-to-filter for every message
// over 64 KB. Instead of failing silently, offer to load the full record on
// demand (via the same raw-download endpoint the "Download full value"
// button and Replay dialog already use) before attempting to parse and
// render the interactive tree. This is deliberately opt-in (a button click)
// rather than automatic like ReplayModal's full-value fetch, since most rows
// are never expanded/filtered and an automatic fetch for every truncated row
// would be wasteful.
//
// Detecting this case can't rely on `m.value_encoding === "json"`: the
// backend detects encoding *after* truncating, and a value cut off
// mid-structure is (almost) never still valid JSON, so the backend itself
// reports `value_encoding: "text"` for exactly these messages. Use
// looksLikeJson (first non-whitespace character only, which truncation
// never removes) instead.
import { useState } from "react";
import {
  base64ToUtf8,
  fetchMessageRawBase64,
  RawValueTooLargeError,
  type Message,
} from "@/lib/api";
import type { Token } from "@/lib/path-builder";
import { looksLikeJson } from "@/lib/looks-like-json";
import { useFormatters } from "@/lib/use-formatters";
import { JsonInteractive } from "@/components/json-interactive";

type FullValueState =
  | { status: "idle" }
  | { status: "loading" }
  | { status: "ready"; parsed: unknown }
  | { status: "error"; message: string };

export function pretty(s: string, enc: string): string {
  if (enc === "json") {
    try {
      return JSON.stringify(JSON.parse(s), null, 2);
    } catch {
      return s;
    }
  }
  return s;
}

export function ValueBody({
  m,
  onPick,
  cluster,
  topic,
}: {
  m: Message;
  onPick: (
    trail: Token[],
    leafValue: unknown,
    arrayLengths: number[],
  ) => void;
  cluster: string;
  topic: string;
}) {
  const fmt = useFormatters();
  const [fullValue, setFullValue] = useState<FullValueState>({
    status: "idle",
  });
  const isJson = m.value_encoding === "json";
  // A value truncated to 64 KB is usually no longer *valid* JSON (cut off
  // mid-structure), so the backend itself reports value_encoding: "text"
  // for it — value_encoding === "json" cannot be trusted to find large,
  // truncated JSON messages. looksLikeJson checks only the first
  // non-whitespace character, which truncation never removes.
  const truncatedLooksLikeJson =
    m.value_truncated === true && looksLikeJson(m.value ?? "");

  if (truncatedLooksLikeJson) {
    if (fullValue.status === "ready") {
      return (
        <div>
          <div className="mb-1 inline-flex items-center gap-1 rounded border border-border bg-accent-subtle px-1.5 py-0.5 text-[10px] text-muted">
            ⌕ click to filter
          </div>
          <JsonInteractive value={fullValue.parsed} onPick={onPick} />
        </div>
      );
    }
    const loadFullValue = async () => {
      setFullValue({ status: "loading" });
      try {
        const base64 = await fetchMessageRawBase64(
          cluster,
          topic,
          m.partition,
          m.offset,
        );
        const parsed = JSON.parse(base64ToUtf8(base64));
        setFullValue({ status: "ready", parsed });
      } catch (err) {
        const message =
          err instanceof RawValueTooLargeError
            ? `full value (${m.value_size_bytes ? fmt.bytes(m.value_size_bytes) : "unknown size"}) exceeds the download limit`
            : err instanceof SyntaxError
              ? "full value could not be parsed as JSON"
              : ((err as Error).message ?? String(err));
        setFullValue({ status: "error", message });
      }
    };
    return (
      <div>
        <pre className="overflow-auto text-xs">
          {pretty(m.value ?? "", m.value_encoding)}
        </pre>
        <div className="mt-2 flex flex-wrap items-center gap-2">
          <button
            type="button"
            onClick={loadFullValue}
            disabled={fullValue.status === "loading"}
            className="rounded border border-border px-2 py-1 text-[11px] hover:border-border-strong disabled:opacity-50"
          >
            {fullValue.status === "loading"
              ? "Loading full value…"
              : "Load full value to enable click-to-filter"}
          </button>
          {fullValue.status === "error" && (
            <span className="text-[11px] text-danger">
              {fullValue.message} — enter the path manually instead.
            </span>
          )}
        </div>
      </div>
    );
  }
  if (isJson && m.value) {
    try {
      const parsed = JSON.parse(m.value);
      return (
        <div>
          <div className="mb-1 inline-flex items-center gap-1 rounded border border-border bg-accent-subtle px-1.5 py-0.5 text-[10px] text-muted">
            ⌕ click to filter
          </div>
          <JsonInteractive value={parsed} onPick={onPick} />
        </div>
      );
    } catch {
      // fall through to pretty()
    }
  }
  return (
    <pre className="overflow-auto text-xs">
      {pretty(m.value ?? "", m.value_encoding)}
    </pre>
  );
}
