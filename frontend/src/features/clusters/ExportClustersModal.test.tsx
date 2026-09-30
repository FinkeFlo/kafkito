import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { decryptExport, encryptExport } from "@/lib/private-clusters-export-crypto";
import { ExportClustersModal } from "./ExportClustersModal";

// The real encryptExport runs unless a test replaces one call.
vi.mock("@/lib/private-clusters-export-crypto", async (importActual) => {
  const actual = await importActual<typeof import("@/lib/private-clusters-export-crypto")>();
  return { ...actual, encryptExport: vi.fn(actual.encryptExport) };
});

const encrypt = vi.mocked(encryptExport);

const PASSPHRASE = "correct horse battery staple";
const PLAINTEXT = JSON.stringify(
  { schema: "kafkito.private-clusters/v1", exported_at: "2026-09-30T00:00:00.000Z", clusters: [] },
  null,
  2,
);

afterEach(() => {
  encrypt.mockClear();
});

function setup(count = 3) {
  const onClose = vi.fn();
  const onEncrypted = vi.fn();
  const { unmount } = render(
    <ExportClustersModal
      count={count}
      plaintext={PLAINTEXT}
      onClose={onClose}
      onEncrypted={onEncrypted}
    />,
  );
  const dialog = screen.getByRole("dialog", { name: "Export private clusters" });
  return {
    user: userEvent.setup(),
    unmount,
    onClose,
    onEncrypted,
    dialog,
    passphrase: within(dialog).getByLabelText("Passphrase"),
    confirmation: within(dialog).getByLabelText("Confirm passphrase"),
    submit: within(dialog).getByRole("button", { name: "Export" }),
  };
}

function deferEncryption() {
  let finish: (file: string) => void = () => {};
  encrypt.mockImplementationOnce(
    () =>
      new Promise<string>((resolve) => {
        finish = resolve;
      }),
  );
  return (file: string) => finish(file);
}

describe("ExportClustersModal", () => {
  it("says what the file holds and that a lost passphrase cannot be recovered", () => {
    const { dialog, passphrase, confirmation } = setup();
    expect(within(dialog).getByRole("status")).toHaveTextContent(
      "The file contains 3 clusters, including their credentials, and is encrypted with this passphrase." +
        "The file cannot be opened without the passphrase, and kafkito cannot recover a lost passphrase.",
    );
    expect(dialog).toHaveAccessibleDescription(/kafkito cannot recover a lost passphrase/);
    expect(passphrase).toHaveAttribute("type", "password");
    expect(confirmation).toHaveAttribute("type", "password");
    expect(passphrase).toHaveAccessibleDescription("At least 12 characters.");
  });

  it("names a single cluster", () => {
    const { dialog } = setup(1);
    expect(within(dialog).getByRole("status")).toHaveTextContent(
      /^The file contains 1 cluster, including/,
    );
  });

  it("refuses a passphrase shorter than 12 characters", async () => {
    const { user, passphrase, confirmation, submit } = setup();
    await user.type(passphrase, "eleven-char");
    await user.type(confirmation, "eleven-char");
    expect(passphrase).not.toHaveAttribute("aria-invalid");

    await user.click(submit);
    expect(passphrase).toHaveAttribute("aria-invalid", "true");
    expect(passphrase).toHaveAccessibleDescription(
      /^At least 12 characters\. .*The passphrase is shorter than 12 characters\.$/,
    );
    expect(encrypt).not.toHaveBeenCalled();

    await user.type(passphrase, "s");
    expect(passphrase).not.toHaveAttribute("aria-invalid");
  });

  it("refuses a confirmation that does not match", async () => {
    const { user, dialog, passphrase, confirmation, submit } = setup();
    await user.type(passphrase, PASSPHRASE);
    await user.type(confirmation, `${PASSPHRASE}.`);
    await user.click(submit);

    expect(within(dialog).getByText("The passphrases do not match.")).toBeInTheDocument();
    expect(confirmation).toHaveAttribute("aria-invalid", "true");
    expect(passphrase).not.toHaveAttribute("aria-invalid");
    expect(encrypt).not.toHaveBeenCalled();

    await user.type(confirmation, "{Backspace}");
    expect(within(dialog).queryByText("The passphrases do not match.")).not.toBeInTheDocument();
  });

  it("encrypts the export with the passphrase and the production key derivation", async () => {
    const { user, passphrase, confirmation, submit, onEncrypted } = setup();
    await user.type(passphrase, PASSPHRASE);
    await user.type(confirmation, PASSPHRASE);
    await user.click(submit);

    await waitFor(() => expect(onEncrypted).toHaveBeenCalledOnce(), { timeout: 5_000 });
    expect(encrypt).toHaveBeenCalledExactlyOnceWith(PLAINTEXT, PASSPHRASE);
    const file = onEncrypted.mock.calls[0][0] as string;
    expect(JSON.parse(file)).toMatchObject({
      format: "kafkito.private-clusters.export",
      version: 2,
      kdf: { name: "PBKDF2", iterations: 600_000 },
    });
    expect(file).not.toContain("kafkito.private-clusters/v1");
    await expect(decryptExport(file, PASSPHRASE)).resolves.toBe(PLAINTEXT);
  });

  it("shows progress while it encrypts", async () => {
    const finish = deferEncryption();
    const { user, dialog, passphrase, confirmation, submit, onEncrypted } = setup();
    await user.type(passphrase, PASSPHRASE);
    await user.type(confirmation, PASSPHRASE);
    await user.click(submit);

    expect(within(dialog).getByRole("button", { name: "Encrypting…" })).toBeDisabled();
    expect(onEncrypted).not.toHaveBeenCalled();
    finish("encrypted file");
    await waitFor(() => expect(onEncrypted).toHaveBeenCalledExactlyOnceWith("encrypted file"));
  });

  it("shows why the export failed and lets the user try again", async () => {
    encrypt.mockRejectedValueOnce(
      new Error("Encrypted exports need a secure connection (HTTPS or localhost)."),
    );
    const { user, dialog, passphrase, confirmation, submit, onEncrypted } = setup();
    await user.type(passphrase, PASSPHRASE);
    await user.type(confirmation, PASSPHRASE);
    await user.click(submit);

    const alert = await within(dialog).findByRole("alert");
    expect(alert).toHaveTextContent(
      "Encrypted exports need a secure connection (HTTPS or localhost).",
    );
    expect(within(alert).getByRole("img", { name: "Error" })).toBeInTheDocument();
    expect(onEncrypted).not.toHaveBeenCalled();

    await user.click(within(dialog).getByRole("button", { name: "Export" }));
    await waitFor(() => expect(onEncrypted).toHaveBeenCalledOnce(), { timeout: 5_000 });
    expect(within(dialog).queryByRole("alert")).not.toBeInTheDocument();
  });

  it("drops the encrypted file when the modal closes before encryption ends", async () => {
    const finish = deferEncryption();
    const { user, unmount, passphrase, confirmation, submit, onEncrypted } = setup();
    await user.type(passphrase, PASSPHRASE);
    await user.type(confirmation, PASSPHRASE);
    await user.click(submit);
    expect(encrypt).toHaveBeenCalledOnce();

    unmount();
    finish("encrypted file");
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(onEncrypted).not.toHaveBeenCalled();
  });

  it("closes on Cancel without encrypting", async () => {
    const { user, dialog, onClose, onEncrypted } = setup();
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(onClose).toHaveBeenCalledOnce();
    expect(encrypt).not.toHaveBeenCalled();
    expect(onEncrypted).not.toHaveBeenCalled();
  });
});
