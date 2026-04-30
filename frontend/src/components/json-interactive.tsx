import { useMemo, useState } from "react";
import type { Token } from "@/lib/path-builder";

export interface JsonInteractiveProps {
  value: unknown;
  onPick: (
    trail: Token[],
    leafValue: unknown,
    arrayLengthAtIndex: number[],
  ) => void;
}

const SIZE_LIMIT_BYTES = 1_000_000;
const ARRAY_COLLAPSE_THRESHOLD = 100;

function approximateSize(v: unknown): number {
  try {
    return JSON.stringify(v).length;
  } catch {
    return Number.POSITIVE_INFINITY;
  }
}

function pretty(v: unknown): string {
  try {
    return JSON.stringify(v, null, 2);
  } catch {
    return String(v);
  }
}

export function JsonInteractive({ value, onPick }: JsonInteractiveProps) {
  const size = useMemo(() => approximateSize(value), [value]);
  if (size > SIZE_LIMIT_BYTES) {
    return (
      <div>
        <div className="mb-2 rounded border border-border bg-tint-amber-bg p-2 text-xs text-muted">
          Nachricht zu groß für interaktiven Modus — Pfad manuell eingeben.
        </div>
        <pre className="overflow-auto rounded border border-border bg-panel p-2 text-xs">
          {pretty(value)}
        </pre>
      </div>
    );
  }
  return (
    <pre className="overflow-auto rounded border border-border bg-panel p-2 text-xs">
      <Node
        node={value}
        trail={[]}
        arrayLengths={[]}
        onPick={onPick}
        indent={0}
      />
    </pre>
  );
}

interface NodeProps {
  node: unknown;
  trail: Token[];
  arrayLengths: number[];
  onPick: (
    trail: Token[],
    leafValue: unknown,
    arrayLengthAtIndex: number[],
  ) => void;
  indent: number;
}

function Node({ node, trail, arrayLengths, onPick, indent }: NodeProps) {
  if (node === null || typeof node !== "object") {
    return (
      <ClickableScalar
        trail={trail}
        arrayLengths={arrayLengths}
        value={node}
        onPick={onPick}
      />
    );
  }
  if (Array.isArray(node)) {
    return (
      <ArrayNode
        arr={node}
        trail={trail}
        arrayLengths={arrayLengths}
        onPick={onPick}
        indent={indent}
      />
    );
  }
  return (
    <ObjectNode
      obj={node as Record<string, unknown>}
      trail={trail}
      arrayLengths={arrayLengths}
      onPick={onPick}
      indent={indent}
    />
  );
}

function ClickableScalar({
  trail,
  arrayLengths,
  value,
  onPick,
}: {
  trail: Token[];
  arrayLengths: number[];
  value: unknown;
  onPick: (
    trail: Token[],
    leafValue: unknown,
    arrayLengthAtIndex: number[],
  ) => void;
}) {
  return (
    <button
      type="button"
      onClick={() => onPick(trail, value, arrayLengths)}
      className="cursor-pointer rounded px-0.5 hover:bg-accent-subtle"
    >
      {value === null
        ? "null"
        : typeof value === "string"
          ? `"${value}"`
          : String(value)}
    </button>
  );
}

function ObjectNode({
  obj,
  trail,
  arrayLengths,
  onPick,
  indent,
}: {
  obj: Record<string, unknown>;
  trail: Token[];
  arrayLengths: number[];
  onPick: (
    trail: Token[],
    leafValue: unknown,
    arrayLengthAtIndex: number[],
  ) => void;
  indent: number;
}) {
  const pad = "  ".repeat(indent);
  const inner = "  ".repeat(indent + 1);
  const entries = Object.entries(obj);
  return (
    <span>
      {"{"}
      {"\n"}
      {entries.map(([k, v], i) => {
        const childTrail: Token[] = [...trail, { kind: "key", name: k }];
        // 0 placeholder; arrays push real length at their level.
        const childLengths = [...arrayLengths, 0];
        return (
          <span key={k}>
            {inner}
            <button
              type="button"
              onClick={() => onPick(childTrail, undefined, childLengths)}
              className="rounded px-0.5 text-accent hover:bg-accent-subtle"
            >
              {`"${k}"`}
            </button>
            {": "}
            <Node
              node={v}
              trail={childTrail}
              arrayLengths={childLengths}
              onPick={onPick}
              indent={indent + 1}
            />
            {i < entries.length - 1 ? "," : ""}
            {"\n"}
          </span>
        );
      })}
      {pad}
      {"}"}
    </span>
  );
}

function ArrayNode({
  arr,
  trail,
  arrayLengths,
  onPick,
  indent,
}: {
  arr: unknown[];
  trail: Token[];
  arrayLengths: number[];
  onPick: (
    trail: Token[],
    leafValue: unknown,
    arrayLengthAtIndex: number[],
  ) => void;
  indent: number;
}) {
  const [expanded, setExpanded] = useState(
    arr.length <= ARRAY_COLLAPSE_THRESHOLD,
  );
  const pad = "  ".repeat(indent);
  const inner = "  ".repeat(indent + 1);
  if (!expanded) {
    return (
      <span>
        {"[ "}
        <button
          type="button"
          onClick={() => setExpanded(true)}
          className="rounded border border-border bg-panel px-2 py-0.5 text-xs hover:border-border-strong"
        >
          {`Show all ${arr.length} items`}
        </button>
        {" ]"}
      </span>
    );
  }
  return (
    <span>
      {"[\n"}
      {arr.map((item, i) => {
        const childTrail: Token[] = [...trail, { kind: "index", value: i }];
        const childLengths = [...arrayLengths, arr.length];
        return (
          <span key={i}>
            {inner}
            <Node
              node={item}
              trail={childTrail}
              arrayLengths={childLengths}
              onPick={onPick}
              indent={indent + 1}
            />
            {i < arr.length - 1 ? "," : ""}
            {"\n"}
          </span>
        );
      })}
      {pad}
      {"]"}
    </span>
  );
}
