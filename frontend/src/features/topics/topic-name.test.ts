import { describe, expect, it } from "vitest";
import { topicNameError } from "./topic-name";

describe("topicNameError", () => {
  it("accepts letters, digits, dots, underscores and dashes", () => {
    expect(topicNameError("orders.v1")).toBeNull();
    expect(topicNameError("payments_retry-2")).toBeNull();
    expect(topicNameError("A".repeat(249))).toBeNull();
  });

  it("asks for a name when it is empty or blank", () => {
    expect(topicNameError("")).toBe("Enter a topic name.");
    expect(topicNameError("   ")).toBe("Enter a topic name.");
  });

  it("rejects characters Kafka does not allow", () => {
    expect(topicNameError("orders v2")).toBe('Only letters, digits, ".", "_" and "-" are allowed.');
    expect(topicNameError("orders/v2")).toBe('Only letters, digits, ".", "_" and "-" are allowed.');
    expect(topicNameError("bestellungen.ä")).toBe(
      'Only letters, digits, ".", "_" and "-" are allowed.',
    );
  });

  it('rejects "." and ".."', () => {
    expect(topicNameError(".")).toBe('A topic cannot be named "." or "..".');
    expect(topicNameError("..")).toBe('A topic cannot be named "." or "..".');
  });

  it("rejects names longer than 249 characters", () => {
    expect(topicNameError("a".repeat(250))).toBe("Use at most 249 characters.");
  });

  it("validates the trimmed name, matching what is sent to the API", () => {
    expect(topicNameError("  orders.v1  ")).toBeNull();
  });
});
