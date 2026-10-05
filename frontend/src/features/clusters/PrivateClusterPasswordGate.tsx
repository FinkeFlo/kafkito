import { useEffect, useId, useState, type FormEvent, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { Button } from "@/components/ui/Button";
import { useFieldError } from "@/components/ui/FieldError";
import { Input } from "@/components/ui/Input";
import { Modal } from "@/components/ui/Modal";
import {
  getPrivateClusterByName,
  missingPasswords,
  needsPasswords,
  setSessionPasswords,
  subscribePrivateClusters,
  type PrivateCluster,
} from "@/lib/private-clusters";
import { invalidatePrivateClusterQueries } from "@/lib/queries/cluster-key";

const labelCls = "text-xs font-semibold uppercase tracking-wider text-muted";

function usePrivateCluster(name: string): PrivateCluster | null {
  const [c, setC] = useState(() => getPrivateClusterByName(name));
  useEffect(() => {
    setC(getPrivateClusterByName(name));
    return subscribePrivateClusters(() => setC(getPrivateClusterByName(name)));
  }, [name]);
  return c;
}

// Renders children unless `cluster` is a private cluster whose passwords
// this tab does not have (it does not remember them). Then it asks for them
// first, so the cluster's pages never send a request without them.
export function PrivateClusterPasswordGate({
  cluster,
  children,
}: {
  cluster: string;
  children: ReactNode;
}) {
  const priv = usePrivateCluster(cluster);
  if (!priv || !needsPasswords(priv)) return <>{children}</>;
  return <PasswordPrompt key={priv.id} cluster={priv} />;
}

function PasswordPrompt({ cluster }: { cluster: PrivateCluster }) {
  const formId = useId();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const missing = missingPasswords(cluster);
  const [password, setPassword] = useState("");
  const [srPassword, setSrPassword] = useState("");
  const [submitted, setSubmitted] = useState(false);
  const pwField = useFieldError(
    submitted && missing.password && password === "" ? "Enter the password." : null,
  );
  const srField = useFieldError(
    submitted && missing.srPassword && srPassword === ""
      ? "Enter the Schema Registry password."
      : null,
  );

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    setSubmitted(true);
    if ((missing.password && password === "") || (missing.srPassword && srPassword === "")) {
      return;
    }
    setSessionPasswords(cluster.id, {
      ...(missing.password ? { password } : {}),
      ...(missing.srPassword ? { srPassword } : {}),
    });
    void invalidatePrivateClusterQueries(qc, cluster.id);
  };

  const onClose = () => void navigate({ to: "/clusters", search: { cluster: undefined } });

  return (
    <Modal
      open
      onClose={onClose}
      title={`Connect to ${cluster.name}`}
      actions={
        <>
          <Button variant="ghost" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" size="sm" type="submit" form={formId}>
            Connect
          </Button>
        </>
      }
    >
      <form id={formId} onSubmit={onSubmit} className="space-y-4">
        <p>
          This browser does not remember the password of this private cluster. It is kept in this
          tab until you close it.
        </p>
        {missing.password && (
          <div>
            <label className="block">
              <span className={labelCls}>Password for {cluster.auth.username}</span>
              <Input
                type="password"
                autoComplete="off"
                autoFocus
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                className="mt-1"
                {...pwField.controlProps}
              />
            </label>
            {pwField.message}
          </div>
        )}
        {missing.srPassword && (
          <div>
            <label className="block">
              <span className={labelCls}>
                Schema Registry password for {cluster.schema_registry?.username}
              </span>
              <Input
                type="password"
                autoComplete="off"
                autoFocus={!missing.password}
                value={srPassword}
                onChange={(e) => setSrPassword(e.target.value)}
                className="mt-1"
                {...srField.controlProps}
              />
            </label>
            {srField.message}
          </div>
        )}
      </form>
    </Modal>
  );
}
