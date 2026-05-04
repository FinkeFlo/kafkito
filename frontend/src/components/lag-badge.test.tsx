import type { ComponentProps } from "react";
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { LagBadge } from "./lag-badge";

const lagThresholdWarning = 1_000;
const lagThresholdDanger = 10_000;
const lagBelowWarning = 999;
const lagZero = 0;
const lagBigintMid = 5_000n;

function setup(props: Partial<ComponentProps<typeof LagBadge>> = {}) {
  return render(<LagBadge value={lagZero} {...props} />);
}

// Intentionally NOT asserting on `aria-hidden="true"` for the glyph span:
// that would couple tests to implementation detail of an a11y attribute the
// user-perceivable behaviour (sr-only label text) already covers, which is a
// docs/TEST_DISCIPLINE.md §1 Fragile Test smell. The sr-only label
// assertions below are the load-bearing a11y contract.
describe("LagBadge", () => {
  it("renders the unknown-lag sr-only label and visible em-dash when value is null (C1)", () => {
    setup({ value: null });

    expect(screen.getByText(/unknown lag/i)).toBeInTheDocument();
    expect(screen.getByText("—")).toBeInTheDocument();
  });

  it("renders the unknown-lag sr-only label and visible em-dash when value is undefined (C2 / M1 mutation-guard)", () => {
    setup({ value: undefined });

    expect(screen.getByText(/unknown lag/i)).toBeInTheDocument();
    expect(screen.getByText("—")).toBeInTheDocument();
  });

  it("renders nothing when value is 0 and showZero is false (C3 / M2 mutation-guard)", () => {
    const { container } = setup({ value: lagZero, showZero: false });

    expect(container).toBeEmptyDOMElement();
    expect(screen.queryByText(/normal lag/i)).toBeNull();
  });

  it("renders neutral 'normal lag' label and visible '0' when value is 0 and showZero is default (C4)", () => {
    setup({ value: lagZero });

    expect(screen.getByText(/normal lag/i)).toBeInTheDocument();
    expect(screen.getByText("0")).toBeInTheDocument();
  });

  it("stays neutral with 'normal lag' label and '·' glyph just below the warning threshold (C5, lagBelowWarning)", () => {
    setup({ value: lagBelowWarning });

    expect(screen.getByText(/normal lag/i)).toBeInTheDocument();
    expect(screen.getByText("·")).toBeInTheDocument();
    expect(screen.getByText("999")).toBeInTheDocument();
  });

  it("flips to warning with 'elevated lag' label and '▲' glyph at the warning threshold (C6, lagThresholdWarning)", () => {
    setup({ value: lagThresholdWarning });

    expect(screen.getByText(/elevated lag/i)).toBeInTheDocument();
    expect(screen.getByText("▲")).toBeInTheDocument();
    expect(screen.getByText("1,000")).toBeInTheDocument();
  });

  it("flips to danger with 'critical lag' label and '▲' glyph at the danger threshold (C7, lagThresholdDanger / M3 mutation-guard)", () => {
    setup({ value: lagThresholdDanger });

    expect(screen.getByText(/critical lag/i)).toBeInTheDocument();
    expect(screen.getByText("▲")).toBeInTheDocument();
    expect(screen.getByText("10,000")).toBeInTheDocument();
  });

  it("surfaces the warning bucket when value is a bigint, locking the user-visible bigint contract (C8)", () => {
    setup({ value: lagBigintMid });

    expect(screen.getByText(/elevated lag/i)).toBeInTheDocument();
    expect(screen.getByText("5,000")).toBeInTheDocument();
  });
});
