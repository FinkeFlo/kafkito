import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { useFieldError } from "./FieldError";
import { Input } from "./Input";

function Field({ error, hintId }: { error: string | null; hintId?: string }) {
  const field = useFieldError(error, hintId);
  return (
    <>
      <Input aria-label="Name" invalid={field.invalid} {...field.controlProps} />
      {hintId ? <span id={hintId}>hint</span> : null}
      {field.message}
    </>
  );
}

describe("useFieldError + Input", () => {
  it("marks the control invalid and describes it with the visible error", () => {
    render(<Field error="Name is required." />);
    const input = screen.getByRole("textbox", { name: "Name" });
    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(input).toHaveAccessibleDescription(/Name is required\.$/);
    expect(screen.getByText("Name is required.")).toBeVisible();
    expect(screen.getByRole("img", { name: "Error" })).toBeInTheDocument();
  });

  it("keeps an existing description next to the error", () => {
    render(<Field error="Too long." hintId="name-hint" />);
    const input = screen.getByRole("textbox", { name: "Name" });
    expect(input.getAttribute("aria-describedby")?.split(" ")[0]).toBe("name-hint");
    expect(input).toHaveAccessibleDescription(/^hint .*Too long\.$/);
  });

  it("sets neither attribute when the field is valid", () => {
    render(<Field error={null} />);
    const input = screen.getByRole("textbox", { name: "Name" });
    expect(input).not.toHaveAttribute("aria-invalid");
    expect(input).not.toHaveAttribute("aria-describedby");
  });

  it("Input sets aria-invalid from the invalid prop alone", () => {
    render(<Input aria-label="Plain" invalid />);
    expect(screen.getByRole("textbox", { name: "Plain" })).toHaveAttribute("aria-invalid", "true");
  });
});
