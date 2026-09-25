// ValueBody — renders a message's value in the messages list. For JSON
// values it shows the interactive, click-to-filter tree (JsonInteractive);
// otherwise it falls back to a pretty-printed <pre> block.
//
// Large values are a special case: the message list only ever holds the
// first 64 KB of a value (`message.value_truncated`), and a truncated JSON
// preview is cut mid-structure, so JSON.parse throws and click-to-filter is
// unavailable for every message over 64 KB. Instead of failing silently,
// offer to load the full record on demand (via the same raw-download
// endpoint the "Download full value" button and the Replay dialog already
// use). This is deliberately opt-in rather than automatic like
// ReplayModal's full-value fetch, since most rows are never expanded and an
// automatic fetch for every truncated row would be wasteful.
//
// Two cases deliberately do *not* offer the button:
//
//   - Schema-Registry values. The list value is the *decoded* JSON rendering,
//     but /raw returns the raw Avro/Protobuf wire bytes, which JSON.parse can
//     never read. Offering the button there would guarantee an error.
//   - Values above JsonInteractive's own SIZE_LIMIT_BYTES. Downloading them
//     would succeed only for the tree renderer to refuse them, so say so up
//     front with a disabled button and a visible reason.
import { useMemo, useState } from "react";
import { base64ToUtf8, RawValueTooLargeError, type Message } from "@/lib/api";
import { prettyValue } from "@/lib/format";
import type { Token } from "@/lib/path-builder";
import { useFormatters } from "@/lib/use-formatters";
import { useMessageRawValue } from "@/lib/use-message-raw-value";
import { JsonInteractive, SIZE_LIMIT_BYTES } from "@/components/json-interactive";
import { Button } from "@/components/button";
import { Notice } from "@/components/Notice";

function ClickToFilterHint() {
  return (
    <div className="mb-1 inline-flex items-center gap-1 rounded-md border border-border bg-accent-subtle px-1.5 py-0.5 text-[10px] text-muted">
      click to filter
    </div>
  );
}

function ValuePre({ m }: { m: Message }) {
  return (
    <pre className="overflow-auto text-xs">{prettyValue(m.value ?? "", m.value_encoding)}</pre>
  );
}

export function ValueBody({
  m,
  onPick,
  cluster,
  topic,
}: {
  m: Message;
  onPick: (trail: Token[], leafValue: unknown) => void;
  cluster: string;
  topic: string;
}) {
  const fmt = useFormatters();

  // Since the search fix, the backend classifies a value cut off
  // mid-structure by its first non-whitespace byte, so `value_encoding`
  // stays "json" for truncated JSON instead of degrading to "text".
  const isTruncatedJson = m.value_truncated === true && m.value_encoding === "json";
  const isSchemaRegistry = !!m.value_sr;
  const tooLargeForTree = (m.value_size_bytes ?? 0) > SIZE_LIMIT_BYTES;
  const canLoadFull = isTruncatedJson && !isSchemaRegistry && !tooLargeForTree;

  // Hooks must run unconditionally, so the query is declared before any of
  // the branches below can return. `enabled` keeps it inert until the user
  // actually asks for the full value.
  const [wantsFull, setWantsFull] = useState(false);
  const rawQuery = useMessageRawValue({
    cluster,
    topic,
    partition: m.partition,
    offset: m.offset,
    enabled: canLoadFull && wantsFull,
  });

  const fullParsed = useMemo(() => {
    if (rawQuery.data === undefined) return undefined;
    try {
      return { ok: true as const, value: JSON.parse(base64ToUtf8(rawQuery.data)) };
    } catch {
      return { ok: false as const };
    }
  }, [rawQuery.data]);

  const inlineParsed = useMemo(() => {
    if (m.value_encoding !== "json" || !m.value || m.value_truncated) return undefined;
    try {
      return { ok: true as const, value: JSON.parse(m.value) };
    } catch {
      return undefined;
    }
  }, [m.value, m.value_encoding, m.value_truncated]);

  if (isTruncatedJson) {
    if (fullParsed?.ok) {
      return (
        <div>
          <p className="sr-only" role="status">
            Full value loaded. Click to filter is now available.
          </p>
          <ClickToFilterHint />
          <JsonInteractive value={fullParsed.value} onPick={onPick} />
        </div>
      );
    }

    const sizeLabel = m.value_size_bytes ? fmt.bytes(m.value_size_bytes) : "unknown size";
    const limitLabel = fmt.bytes(SIZE_LIMIT_BYTES);

    return (
      <div>
        <ValuePre m={m} />
        <div className="mt-2 flex flex-col gap-2">
          {isSchemaRegistry ? (
            <p className="text-[11px] text-muted">
              Click to filter is not available for Schema Registry values — the full record is only
              downloadable in its encoded wire format. Enter the path manually instead.
            </p>
          ) : tooLargeForTree ? (
            <>
              <div>
                <Button
                  variant="secondary"
                  size="sm"
                  disabled
                  aria-describedby={`value-too-large-${m.partition}-${m.offset}`}
                >
                  Load full value to enable click-to-filter
                </Button>
              </div>
              <p
                id={`value-too-large-${m.partition}-${m.offset}`}
                className="text-[11px] text-muted"
              >
                Full value is {sizeLabel} — above the {limitLabel} interactive limit. Enter the path
                manually instead.
              </p>
            </>
          ) : (
            <>
              <div>
                <Button
                  variant="secondary"
                  size="sm"
                  loading={rawQuery.isFetching}
                  onClick={() => setWantsFull(true)}
                >
                  {rawQuery.isFetching
                    ? "Loading full value"
                    : "Load full value to enable click-to-filter"}
                </Button>
              </div>
              {!rawQuery.isFetching && (rawQuery.error || fullParsed?.ok === false) && (
                <Notice
                  intent="danger"
                  actions={
                    <Button variant="secondary" size="sm" onClick={() => void rawQuery.refetch()}>
                      Retry
                    </Button>
                  }
                >
                  {rawQuery.error
                    ? rawQuery.error instanceof RawValueTooLargeError
                      ? `Full value (${sizeLabel}) exceeds the download limit. Enter the path manually instead.`
                      : `${rawQuery.error.message} — enter the path manually instead.`
                    : "Full value could not be parsed as JSON. Enter the path manually instead."}
                </Notice>
              )}
            </>
          )}
        </div>
      </div>
    );
  }

  if (inlineParsed?.ok) {
    return (
      <div>
        <ClickToFilterHint />
        <JsonInteractive value={inlineParsed.value} onPick={onPick} />
      </div>
    );
  }

  return <ValuePre m={m} />;
}
