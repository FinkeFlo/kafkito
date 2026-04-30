import { useQuery, useQueryClient } from '@tanstack/react-query';
import { createContext, useContext, type ReactNode } from 'react';
import { apiFetch } from './api';
import type { CurrentUser, Me } from './types';

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

  const cu = useQuery({
    queryKey: ['auth', 'currentUser'],
    queryFn: async (): Promise<CurrentUser> => {
      const res = await apiFetch('/user-api/currentUser');
      if (!res.ok) throw new Error('currentUser ' + res.status);
      return (await res.json()) as CurrentUser;
    },
    staleTime: 10 * 60 * 1000,
    retry: false,
  });

  const me = useQuery({
    queryKey: ['auth', 'me'],
    queryFn: async (): Promise<Me> => {
      const res = await apiFetch('/api/v1/me');
      if (!res.ok) throw new Error('me ' + res.status);
      return (await res.json()) as Me;
    },
    staleTime: 5 * 60 * 1000,
    retry: false,
  });

  const value: AuthContextValue = {
    currentUser: cu.data,
    me: me.data,
    isAuthenticated: Boolean(cu.data && me.data && !me.data.anonymous),
    isLoading: cu.isLoading || me.isLoading,
    refresh: async () => {
      await qc.invalidateQueries({ queryKey: ['auth'] });
    },
  };

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuthContext(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error('useAuthContext: AuthProvider missing');
  return ctx;
}
