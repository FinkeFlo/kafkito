import { useAuthContext } from './AuthProvider';

export function useAuth() {
  return useAuthContext();
}

export function useCurrentUser() {
  return useAuthContext().currentUser;
}

export function useScope(scope: string): boolean {
  const me = useAuthContext().me;
  return Boolean(me?.scopes?.includes(scope));
}
