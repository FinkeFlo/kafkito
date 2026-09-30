import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { decryptExport, encryptExport } from "@/lib/private-clusters-export-crypto";
import { ImportPassphraseModal } from "./ImportPassphraseModal";

// The real decryptExport runs unless a test replaces one call.
vi.mock("@/lib/private-clusters-export-crypto", async (importActual) => {
  const actual = await importActual<typeof import("@/lib/private-clusters-export-crypto")>();
  return { ...actual, decryptExport: vi.fn(actual.decryptExport) };
});

const decrypt = vi.mocked(decryptExport);

const PASSPHRASE = "correct horse battery staple";
const PLAINTEXT = '{"schema":"kafkito.private-clusters/v1","clusters":[]}';
const FILE_NAME = "kafkito-private-clusters-2026-09-30.json";

let file = "";

beforeAll(async () => {
  // Far below the production 600,000 iterations so the tests stay fast.
  file = await encryptExport(PLAINTEXT, PASSPHRASE, { iterations: 1_000 });
});

afterEach(() => {
  decrypt.mockClear();
});

function setup(fileText = file) {
  const onClose = vi.fn();
  const onDecrypted = vi.fn();
  const { unmount } = render(
    <ImportPassphraseModal
      fileName={FILE_NAME}
      fileText={fileText}
      onClose={onClose}
      onDecrypted={onDecrypted}
    />,
  );
  const dialog = screen.getByRole("dialog", { name: "Import private clusters" });
  return {
    user: userEvent.setup(),
    unmount,
    onClose,
    onDecrypted,
    dialog,
    passphrase: within(dialog).getByLabelText("Passphrase"),
    submit: within(dialog).getByRole("button", { name: "Import" }),
  };
}

describe("ImportPassphraseModal", () => {
  it("names the encrypted file and asks for its passphrase", () => {
    const { dialog, passphrase } = setup();
    expect(dialog).toHaveTextContent(
      `${FILE_NAME} is encrypted. Enter the passphrase it was exported with.`,
    );
    expect(passphrase).toHaveAttribute("type", "password");
  });

  it("asks for a passphrase before decrypting", async () => {
    const { user, dialog, passphrase, submit, onDecrypted } = setup();
    await user.click(submit);
    expect(within(dialog).getByText("Enter the passphrase.")).toBeInTheDocument();
    expect(passphrase).toHaveAttribute("aria-invalid", "true");
    expect(decrypt).not.toHaveBeenCalled();
    expect(onDecrypted).not.toHaveBeenCalled();
  });

  it("stays open on a wrong passphrase and decrypts with the right one", async () => {
    const { user, dialog, passphrase, submit, onDecrypted } = setup();
    await user.type(passphrase, "not the passphrase");
    await user.click(submit);

    const alert = await within(dialog).findByRole("alert");
    expect(alert).toHaveTextContent("Wrong passphrase or damaged file.");
    expect(within(alert).getByRole("img", { name: "Error" })).toBeInTheDocument();
    expect(passphrase).toHaveFocus();
    expect(onDecrypted).not.toHaveBeenCalled();

    await user.clear(passphrase);
    await user.type(passphrase, `${PASSPHRASE}{Enter}`);
    await waitFor(() => expect(onDecrypted).toHaveBeenCalledExactlyOnceWith(PLAINTEXT));
  });

  it("reports a damaged file with the same message", async () => {
    const envelope = JSON.parse(file) as { data: string };
    const first = envelope.data[0] === "A" ? "B" : "A";
    const damaged = JSON.stringify({ ...envelope, data: first + envelope.data.slice(1) });
    const { user, dialog, passphrase, submit, onDecrypted } = setup(damaged);
    await user.type(passphrase, PASSPHRASE);
    await user.click(submit);

    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "Wrong passphrase or damaged file.",
    );
    expect(onDecrypted).not.toHaveBeenCalled();
  });

  it("drops the decrypted bundle when the modal closes before decryption ends", async () => {
    let finish: (plaintext: string) => void = () => {};
    decrypt.mockImplementationOnce(
      () =>
        new Promise<string>((resolve) => {
          finish = resolve;
        }),
    );
    const { user, unmount, dialog, passphrase, submit, onDecrypted } = setup();
    await user.type(passphrase, PASSPHRASE);
    await user.click(submit);
    expect(within(dialog).getByRole("button", { name: "Decrypting…" })).toBeDisabled();

    unmount();
    finish(PLAINTEXT);
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(onDecrypted).not.toHaveBeenCalled();
  });

  it("closes on Cancel", async () => {
    const { user, dialog, onClose, onDecrypted } = setup();
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(onClose).toHaveBeenCalledOnce();
    expect(onDecrypted).not.toHaveBeenCalled();
  });
});
