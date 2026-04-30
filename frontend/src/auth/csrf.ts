// CSRF token cache. The approuter issues a fresh token on a HEAD request to
// any auth-required path with header `x-csrf-token: fetch`; the response
// header `x-csrf-token` carries the token to echo on subsequent writes.

let cachedToken: string | null = null;

export async function getCsrfToken(): Promise<string> {
  if (cachedToken) return cachedToken;
  const res = await fetch('/', {
    method: 'HEAD',
    credentials: 'include',
    headers: { 'x-csrf-token': 'fetch' },
  });
  const tok = res.headers.get('x-csrf-token');
  if (!tok) throw new Error('no csrf token in response');
  cachedToken = tok;
  return tok;
}

export function clearCsrfToken(): void {
  cachedToken = null;
}
