import { clearCsrfToken, getCsrfToken } from './csrf';

export class SessionExpiredError extends Error {
  constructor() {
    super('session expired');
  }
}

const writeMethods = new Set(['POST', 'PUT', 'PATCH', 'DELETE']);

// apiFetch is a fetch wrapper for SPA<->approuter<->backend traffic:
// - always sends cookies (approuter session)
// - injects x-csrf-token on writes
// - on 401: clears CSRF cache, navigates the top window to / so the
//   approuter can run the OAuth code flow against XSUAA, throws
//   SessionExpiredError so callers do not consume an undefined Response
// - on 403 with "x-csrf-token: Required": single retry with a fresh token
export async function apiFetch(input: RequestInfo, init: RequestInit = {}): Promise<Response> {
  const method = (init.method ?? 'GET').toUpperCase();
  const headers = new Headers(init.headers);
  if (writeMethods.has(method)) {
    try {
      headers.set('x-csrf-token', await getCsrfToken());
    } catch {
      // CSRF endpoint unreachable (e.g., devauth backend without approuter, or
      // approuter not yet deployed). Send the write anyway; in production the
      // 403 retry branch below will re-fetch the token, in dev the backend has
      // no CSRF enforcement so the write succeeds as-is.
    }
  }
  const res = await fetch(input, { ...init, method, headers, credentials: 'include' });
  if (res.status === 401) {
    clearCsrfToken();
    window.location.assign('/');
    throw new SessionExpiredError();
  }
  if (res.status === 403 && res.headers.get('x-csrf-token') === 'Required') {
    clearCsrfToken();
    return apiFetch(input, init);
  }
  return res;
}
