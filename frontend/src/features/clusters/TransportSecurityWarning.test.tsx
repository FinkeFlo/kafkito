import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import { TransportSecurityWarning } from "./TransportSecurityWarning";

afterEach(cleanup);

describe("TransportSecurityWarning", () => {
  it("says that SASL/PLAIN without TLS sends the credentials in cleartext and is rejected", () => {
    render(<TransportSecurityWarning authType="plain" tlsEnabled={false} tlsInsecure={false} />);
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent(
      "SASL/PLAIN without TLS sends the username and password in cleartext, and all data as well. kafkito rejects SASL/PLAIN without TLS for private clusters unless the operator allows it.",
    );
    expect(within(alert).getByRole("img", { name: "Warning" })).toBeInTheDocument();
  });

  it.each(["none", "scram-sha-256", "scram-sha-512"] as const)(
    "says that data travels unencrypted without TLS (auth %s)",
    (authType) => {
      render(
        <TransportSecurityWarning authType={authType} tlsEnabled={false} tlsInsecure={false} />,
      );
      const alert = screen.getByRole("alert");
      expect(alert).toHaveTextContent("Without TLS, all data travels unencrypted.");
      expect(alert).not.toHaveTextContent(/password/);
      expect(alert).not.toHaveTextContent(/rejects/);
      expect(within(alert).getByRole("img", { name: "Warning" })).toBeInTheDocument();
    },
  );

  // Skip verify has no effect without TLS; the unencrypted warning covers it.
  it("warns about the unencrypted connection only when TLS is off with Skip verify", () => {
    render(<TransportSecurityWarning authType="none" tlsEnabled={false} tlsInsecure />);
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Without TLS, all data travels unencrypted.",
    );
    expect(screen.queryByText(/certificate/)).not.toBeInTheDocument();
  });

  it("says that Skip verify lets the connection be intercepted", () => {
    render(<TransportSecurityWarning authType="scram-sha-512" tlsEnabled tlsInsecure />);
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent(
      "With Skip verify, the broker's certificate is not checked, so the connection can be intercepted.",
    );
    expect(within(alert).getByRole("img", { name: "Warning" })).toBeInTheDocument();
  });

  it.each(["none", "plain", "scram-sha-256"] as const)(
    "renders nothing with verified TLS (auth %s)",
    (authType) => {
      const { container } = render(
        <TransportSecurityWarning authType={authType} tlsEnabled tlsInsecure={false} />,
      );
      expect(container).toBeEmptyDOMElement();
    },
  );
});
