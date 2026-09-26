import { useQuery, useQueryClient } from "@tanstack/react-query";
import { createContext, useContext, type ReactNode } from "react";
import { authKeys, authQueries } from "../lib/queries/auth";
import type { CurrentUser, Me } from "./types";

interface AuthContextValue {
  currentUser: CurrentUser | undefined;
  me: Me | undefined;
  isAuthenticated: boolean;
  isLoading: boolean;
  refresh: () => Promise<void>;
}

const AuthContext = createContext<AuthContextValue | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const qc = useQueryClient();

  const cu = useQuery(authQueries.currentUser());

  const me = useQuery(authQueries.me());

  const value: AuthContextValue = {
    currentUser: cu.data,
    me: me.data,
    isAuthenticated: Boolean(cu.data && me.data && !me.data.anonymous),
    isLoading: cu.isLoading || me.isLoading,
    refresh: async () => {
      await qc.invalidateQueries({ queryKey: authKeys.all });
    },
  };

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuthContext(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuthContext: AuthProvider missing");
  return ctx;
}
