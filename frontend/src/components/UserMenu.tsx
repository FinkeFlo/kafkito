import { useAuth } from '../auth/hooks';

export function UserMenu() {
  const { currentUser, me, isLoading } = useAuth();
  if (isLoading) return <span className="text-muted text-xs">…</span>;
  if (!currentUser) return null;

  const display =
    currentUser.displayName || currentUser.name || currentUser.email;

  return (
    <div className="flex items-center gap-3 text-sm">
      <div className="flex flex-col text-right">
        <span className="text-text text-[13px]">{display}</span>
        {me?.scopes?.length ? (
          <span className="text-muted text-[11px]">
            {me.scopes.join(', ')}
          </span>
        ) : null}
      </div>
      <a
        href="/logout"
        className="rounded-md border border-border bg-panel px-3 py-1 text-xs text-muted transition-colors hover:bg-hover hover:text-text"
      >
        Logout
      </a>
    </div>
  );
}
