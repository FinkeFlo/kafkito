import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { StatusDot } from "./StatusDot";
import { StatusBox, StatusIcon } from "./StatusIcon";
import { KpiCard } from "./KpiCard";
import { Notice } from "./Notice";

// WCAG 1.4.1: every status must carry a non-colour cue. These tests pin
// the cue (accessible name + distinct shape) so a refactor back to a bare
// coloured dot or tinted box fails here.

function shapeOf(container: HTMLElement): string {
  const root = container.firstElementChild as HTMLElement;
  return root.innerHTML.replace(/\b(?:text|bg|border)-[a-z-]+/g, "");
}

describe("StatusDot", () => {
  it("names reachable and unreachable states", () => {
    render(
      <>
        <StatusDot reachable />
        <StatusDot reachable={false} />
        <StatusDot />
      </>,
    );
    expect(screen.getByRole("img", { name: "healthy" })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "unhealthy" })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "unknown" })).toBeInTheDocument();
  });

  it("draws a different shape per intent, not just a different colour", () => {
    const shapes = (["success", "warning", "danger", "neutral"] as const).map((intent) =>
      shapeOf(render(<StatusDot intent={intent} hideLabel />).container),
    );
    expect(new Set(shapes).size).toBe(shapes.length);
  });

  it("hides the whole indicator when the caller labels it", () => {
    const { container } = render(<StatusDot reachable hideLabel />);
    expect(container.firstElementChild).toHaveAttribute("aria-hidden", "true");
    expect(container.firstElementChild).not.toHaveAttribute("role");
  });
});

describe("StatusIcon / StatusBox", () => {
  it("labels each outcome and uses a distinct glyph", () => {
    const intents = ["success", "warning", "danger", "info"] as const;
    const glyphs = intents.map((intent) => {
      const { container } = render(<StatusIcon intent={intent} />);
      return container.querySelector("svg")?.getAttribute("class");
    });
    expect(new Set(glyphs).size).toBe(intents.length);
    for (const name of ["Success", "Warning", "Error", "Info"]) {
      expect(screen.getByRole("img", { name })).toBeInTheDocument();
    }
  });

  it("announces errors as alerts with a labelled icon", () => {
    render(<StatusBox intent="danger">boom</StatusBox>);
    const box = screen.getByRole("alert");
    expect(box).toHaveTextContent("boom");
    expect(screen.getByRole("img", { name: "Error" })).toBeInTheDocument();
  });

  it("keeps success boxes polite", () => {
    render(<StatusBox intent="success">done</StatusBox>);
    expect(screen.getByRole("status")).toHaveTextContent("done");
    expect(screen.getByRole("img", { name: "Success" })).toBeInTheDocument();
  });
});

describe("KpiCard delta", () => {
  it("pairs good and bad deltas with a labelled icon", () => {
    render(
      <>
        <KpiCard label="a" value={0} delta="none" deltaIntent="good" />
        <KpiCard label="b" value={1} delta="prod" deltaIntent="bad" />
        <KpiCard label="c" value={2} delta="n/a" />
      </>,
    );
    expect(screen.getByRole("img", { name: "Good" })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "Needs attention" })).toBeInTheDocument();
    expect(screen.getAllByRole("img")).toHaveLength(2);
    expect(screen.queryByText(/^[+−±]/)).toBeNull();
  });
});

describe("Notice", () => {
  it("exposes the intent through a labelled icon", () => {
    render(<Notice intent="warning">careful</Notice>);
    expect(screen.getByRole("img", { name: "Warning" })).toBeInTheDocument();
  });

  it("keeps a custom icon decorative", () => {
    render(
      <Notice intent="info" icon={<span>i</span>}>
        fyi
      </Notice>,
    );
    expect(screen.queryByRole("img")).toBeNull();
  });
});
