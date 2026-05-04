import { act, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { Timestamp } from "./timestamp";
import { setTimeZone } from "@/lib/use-timezone";

const tzKey = "kafkito.timezone";
const fixedDateUtc = new Date("2026-05-04T08:00:00.000Z");
const fixedIsoUtc = "2026-05-04T08:00:00.000Z";

afterEach(() => {
  window.localStorage.removeItem(tzKey);
});

function findTimeElement(): HTMLTimeElement {
  const el = document.querySelector("time");
  if (!el) throw new Error("expected a <time> element to be rendered");
  return el as HTMLTimeElement;
}

describe("Timestamp", () => {
  it("renders the formatted ISO string in the global zone with matching dateTime + title attrs in UTC (T1)", () => {
    setTimeZone("utc");

    render(<Timestamp value={fixedDateUtc} />);

    expect(screen.getByText(fixedIsoUtc)).toBeInTheDocument();
    const timeEl = findTimeElement();
    expect(timeEl.getAttribute("dateTime")).toBe(fixedIsoUtc);
    expect(timeEl.getAttribute("title")).toBe(fixedIsoUtc);
  });

  it("uses the explicit zone prop in preference to the global zone (T2 / M-zone-override mutation-guard)", () => {
    setTimeZone("local");

    render(<Timestamp value={fixedDateUtc} zone="utc" />);

    expect(screen.getByText(fixedIsoUtc)).toBeInTheDocument();
  });

  it("re-renders to follow the global zone when setTimeZone fires (T3)", () => {
    setTimeZone("local");
    const { rerender: _rerender } = render(<Timestamp value={fixedDateUtc} />);
    const localText = findTimeElement().textContent;

    act(() => {
      setTimeZone("utc");
    });

    expect(screen.getByText(fixedIsoUtc)).toBeInTheDocument();
    expect(findTimeElement().textContent).not.toBe(localText);
  });

  it.each<[string, unknown]>([
    ["null", null],
    ["undefined", undefined],
    ["empty-string", ""],
  ])("renders '—' with no dateTime and no title attrs when value is %s (T4)", (_label, value) => {
    render(<Timestamp value={value as Parameters<typeof Timestamp>[0]["value"]} />);

    expect(screen.getByText("—")).toBeInTheDocument();
    const timeEl = findTimeElement();
    expect(timeEl.hasAttribute("dateTime")).toBe(false);
    expect(timeEl.hasAttribute("title")).toBe(false);
  });

  it("renders '—' for an unparseable date input and omits the dateTime + title attrs (T5)", () => {
    render(<Timestamp value="not-a-date" />);

    expect(screen.getByText("—")).toBeInTheDocument();
    const timeEl = findTimeElement();
    expect(timeEl.hasAttribute("dateTime")).toBe(false);
    expect(timeEl.hasAttribute("title")).toBe(false);
  });
});
