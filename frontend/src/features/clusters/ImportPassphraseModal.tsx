import { useEffect, useId, useRef, useState, type FormEvent } from "react";
import { Button } from "@/components/ui/Button";
import { useFieldError } from "@/components/ui/FieldError";
import { Input } from "@/components/ui/Input";
import { Modal } from "@/components/ui/Modal";
import { StatusBox } from "@/components/ui/StatusIcon";
import { decryptExport } from "@/lib/private-clusters-export-crypto";

const labelCls = "text-xs font-semibold uppercase tracking-wider text-muted";

// Asks for the passphrase of an encrypted private-cluster export and hands
// the decrypted bundle JSON to onDecrypted. A wrong passphrase or a damaged
// file keeps the modal open with one generic message. Closing the modal
// while it decrypts drops the result, so nothing is imported after Cancel.
export function ImportPassphraseModal({
  fileName,
  fileText,
  onClose,
  onDecrypted,
}: {
  fileName: string;
  fileText: string;
  onClose: () => void;
  onDecrypted: (plaintext: string) => void;
}) {
  const formId = useId();
  const inputRef = useRef<HTMLInputElement>(null);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const [passphrase, setPassphrase] = useState("");
  const [submitted, setSubmitted] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const field = useFieldError(submitted && passphrase === "" ? "Enter the passphrase." : null);

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setSubmitted(true);
    if (passphrase === "" || busy) return;
    setBusy(true);
    setFailure(null);
    let plaintext: string;
    try {
      plaintext = await decryptExport(fileText, passphrase);
    } catch (err) {
      if (!mounted.current) return;
      setFailure((err as Error).message);
      setBusy(false);
      inputRef.current?.focus();
      return;
    }
    if (mounted.current) onDecrypted(plaintext);
  };

  return (
    <Modal
      open
      onClose={onClose}
      title="Import private clusters"
      actions={
        <>
          <Button variant="ghost" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" size="sm" type="submit" form={formId} loading={busy}>
            {busy ? "Decrypting…" : "Import"}
          </Button>
        </>
      }
    >
      <form id={formId} onSubmit={onSubmit} className="space-y-4">
        <p>
          <span className="break-all font-mono">{fileName}</span> is encrypted. Enter the passphrase
          it was exported with.
        </p>
        <div>
          <label className="block">
            <span className={labelCls}>Passphrase</span>
            <Input
              ref={inputRef}
              type="password"
              autoComplete="off"
              value={passphrase}
              onChange={(e) => setPassphrase(e.target.value)}
              className="mt-1"
              {...field.controlProps}
            />
          </label>
          {field.message}
        </div>
        {failure && <StatusBox intent="danger">{failure}</StatusBox>}
      </form>
    </Modal>
  );
}
