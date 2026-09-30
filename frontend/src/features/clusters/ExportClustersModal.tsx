import { useEffect, useId, useRef, useState, type FormEvent } from "react";
import { Button } from "@/components/ui/Button";
import { useFieldError } from "@/components/ui/FieldError";
import { Input } from "@/components/ui/Input";
import { Modal } from "@/components/ui/Modal";
import { Notice } from "@/components/ui/Notice";
import { StatusBox } from "@/components/ui/StatusIcon";
import {
  MIN_PASSPHRASE_LENGTH,
  encryptExport,
  isPassphraseLongEnough,
} from "@/lib/private-clusters-export-crypto";

const labelCls = "text-xs font-semibold uppercase tracking-wider text-muted";

// Asks for the passphrase that encrypts a private-cluster export and hands
// the encrypted file text to onEncrypted. Closing the modal while it
// encrypts drops the result, so nothing is downloaded after Cancel.
export function ExportClustersModal({
  count,
  plaintext,
  onClose,
  onEncrypted,
}: {
  count: number;
  plaintext: string;
  onClose: () => void;
  onEncrypted: (file: string) => void;
}) {
  const formId = useId();
  const noteId = useId();
  const hintId = useId();
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const [passphrase, setPassphrase] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [submitted, setSubmitted] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  const tooShort = !isPassphraseLongEnough(passphrase);
  const mismatch = confirmation !== passphrase;
  const passphraseField = useFieldError(
    submitted && tooShort
      ? `The passphrase is shorter than ${MIN_PASSPHRASE_LENGTH} characters.`
      : null,
    hintId,
  );
  const confirmationField = useFieldError(
    submitted && mismatch ? "The passphrases do not match." : null,
  );

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setSubmitted(true);
    if (tooShort || mismatch || busy) return;
    setBusy(true);
    setFailure(null);
    let file: string;
    try {
      file = await encryptExport(plaintext, passphrase);
    } catch (err) {
      if (!mounted.current) return;
      setFailure((err as Error).message);
      setBusy(false);
      return;
    }
    if (mounted.current) onEncrypted(file);
  };

  return (
    <Modal
      open
      onClose={onClose}
      title="Export private clusters"
      ariaDescribedBy={noteId}
      actions={
        <>
          <Button variant="ghost" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" size="sm" type="submit" form={formId} loading={busy}>
            {busy ? "Encrypting…" : "Export"}
          </Button>
        </>
      }
    >
      <form id={formId} onSubmit={onSubmit} className="space-y-4">
        <div id={noteId}>
          <Notice intent="info">
            <p>
              {`The file contains ${count === 1 ? "1 cluster" : `${count} clusters`}, including their credentials, and is encrypted with this passphrase.`}
            </p>
            <p className="mt-1">
              The file cannot be opened without the passphrase, and kafkito cannot recover a lost
              passphrase.
            </p>
          </Notice>
        </div>
        <div>
          <label className="block">
            <span className={labelCls}>Passphrase</span>
            <Input
              type="password"
              autoComplete="new-password"
              value={passphrase}
              onChange={(e) => setPassphrase(e.target.value)}
              className="mt-1"
              {...passphraseField.controlProps}
            />
          </label>
          <p id={hintId} className="mt-1 text-xs text-muted">
            At least {MIN_PASSPHRASE_LENGTH} characters.
          </p>
          {passphraseField.message}
        </div>
        <div>
          <label className="block">
            <span className={labelCls}>Confirm passphrase</span>
            <Input
              type="password"
              autoComplete="new-password"
              value={confirmation}
              onChange={(e) => setConfirmation(e.target.value)}
              className="mt-1"
              {...confirmationField.controlProps}
            />
          </label>
          {confirmationField.message}
        </div>
        {failure && <StatusBox intent="danger">{failure}</StatusBox>}
      </form>
    </Modal>
  );
}
