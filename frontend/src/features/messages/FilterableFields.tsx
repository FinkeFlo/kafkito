import { MaskedBadge } from "./MaskedBadge";
import type { FieldFilter } from "./use-click-to-filter";

// The same affordance as the keys and values of the JSON view: the text
// itself is the button, highlighted on hover. py-1 and min-w-6 make every
// target at least 24 x 24 px (WCAG 2.5.8, axe target-size), so stacked
// header lines need no extra spacing.
const pickable = "min-w-6 cursor-pointer rounded px-0.5 py-1 text-left hover:bg-accent-subtle";

/**
 * Header lines whose key and value start a search when clicked: the key
 * searches for records that carry the header, the value for that exact
 * value. A masked value is plain text: its redacted form would match every
 * masked record.
 */
export function HeaderLines({
  id,
  headers,
  masked,
  onFilter,
}: {
  id?: string;
  headers: Record<string, string>;
  masked: Set<string>;
  onFilter: (filter: FieldFilter) => void;
}) {
  return (
    <ul
      id={id}
      className="max-h-96 space-y-0.5 overflow-auto rounded-md bg-subtle p-3 font-mono text-[11px] leading-relaxed text-text"
    >
      {Object.entries(headers).map(([k, v]) => (
        <li key={k} data-testid="header-line" className="break-all whitespace-pre-wrap">
          <button
            type="button"
            title="Search by this header"
            aria-label={`Search by header ${k}`}
            onClick={() => onFilter({ kind: "header", key: k })}
            className={`${pickable} text-accent`}
          >
            {k}
          </button>
          {": "}
          {masked.has(k) ? (
            <>
              {v} <MaskedBadge title="Header value modified by a data_masking rule" />
            </>
          ) : (
            <button
              type="button"
              title="Search by this value"
              aria-label={`Search by ${k} = ${v === "" ? "(empty)" : v}`}
              onClick={() => onFilter({ kind: "header", key: k, value: v })}
              className={pickable}
            >
              {v === "" ? <span className="italic text-muted">(empty)</span> : v}
            </button>
          )}
        </li>
      ))}
    </ul>
  );
}

/**
 * The record key, shown as `display` (pretty-printed where it is JSON); a
 * click searches for the key as the list renders it (`value`).
 */
export function RecordKey({
  display,
  value,
  onFilter,
}: {
  display: string;
  value: string;
  onFilter: (filter: FieldFilter) => void;
}) {
  return (
    <pre className="max-h-96 overflow-auto whitespace-pre-wrap break-all rounded-md bg-subtle p-3 font-mono text-[11px] leading-relaxed text-text">
      <button
        type="button"
        title="Search by this key"
        aria-label={`Search by key ${value}`}
        onClick={() => onFilter({ kind: "key", value })}
        className={pickable}
      >
        {display}
      </button>
    </pre>
  );
}
