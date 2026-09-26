// Typed HTTP client for the Kafkito API (openapi-fetch over the types
// generated from api/openapi.yaml into ./api.gen.ts).
//
// Everything request-scoped lives here, once:
// - the cluster path segment: shared clusters use their (percent-encoded)
//   name, private (browser-stored) clusters the reserved sentinel;
// - the headers, set by a single onRequest middleware: X-Kafkito-Cluster
//   for private clusters, X-Kafkito-Confirm-Prod for confirmed writes;
// - non-2xx responses, turned into an HttpError by onResponse so the
//   endpoint functions in ./api.ts can map them to the messages the UI shows.
// Transport concerns (cookies, x-requested-with, CSRF, 401 redirect) stay in
// apiFetch, which is the client's fetch implementation.

import createClient, { defaultPathSerializer, type Middleware } from "openapi-fetch";
import { apiFetch } from "../auth/api";
import type { components, paths } from "./api.gen";
import {
  PRIVATE_CLUSTER_SENTINEL,
  encodePrivateClusterHeader,
  getPrivateClusterByName,
} from "./private-clusters";

const CLUSTER_HEADER = "X-Kafkito-Cluster";

// Must match internal/server/prod_confirm.go's ProdConfirmHeader. Sent only
// after the user confirms the production warning dialog; the backend is the
// actual enforcement point — it rejects mutating calls against is_prod
// clusters that omit this header, regardless of what this client's local
// cluster list believes.
const PROD_CONFIRM_HEADER = "X-Kafkito-Confirm-Prod";

/**
 * A non-2xx API response. Carries the raw body so callers can reproduce
 * their error message exactly; the message itself never includes request
 * headers.
 */
export class HttpError extends Error {
  constructor(
    readonly status: number,
    readonly statusText: string,
    readonly body: string,
  ) {
    super(`HTTP ${status}`);
  }

  /** The body parsed as the API's Error schema, or null if it isn't JSON. */
  json(): Partial<components["schemas"]["Error"]> | null {
    try {
      return JSON.parse(this.body) as Partial<components["schemas"]["Error"]>;
    } catch {
      return null;
    }
  }
}

const prodConfirmed = new WeakSet<object>();

/**
 * Marks a request's `params` as confirmed for a production cluster; the
 * header middleware then sends X-Kafkito-Confirm-Prod. Returns `params`.
 */
export function withProdConfirm<P extends object>(params: P, confirm: boolean): P {
  if (confirm) prodConfirmed.add(params);
  return params;
}

function privateCluster(pathParams: Record<string, unknown> | undefined) {
  const name = pathParams?.cluster;
  return typeof name === "string" ? getPrivateClusterByName(name) : null;
}

const kafkitoMiddleware: Middleware = {
  onRequest({ request, params }) {
    const priv = privateCluster(params.path);
    if (priv) request.headers.set(CLUSTER_HEADER, encodePrivateClusterHeader(priv));
    if (prodConfirmed.has(params)) request.headers.set(PROD_CONFIRM_HEADER, "true");
    return request;
  },
  async onResponse({ response }) {
    if (!response.ok) {
      throw new HttpError(response.status, response.statusText, await response.text());
    }
    return undefined;
  },
};

// openapi-fetch hands over a Request; apiFetch takes (url, init) so it can
// replay the request after a CSRF refresh. The body is buffered because
// streaming request bodies are not supported by every browser.
async function transport(request: Request): Promise<Response> {
  return apiFetch(request.url, {
    method: request.method,
    headers: request.headers,
    body: request.body ? await request.blob() : undefined,
    signal: request.signal,
  });
}

export const client = createClient<paths>({
  fetch: transport,
  // Callers pass the display cluster name; private clusters go out as the
  // sentinel, everything else with openapi-fetch's encodeURIComponent.
  pathSerializer(pathname, pathParams) {
    return defaultPathSerializer(
      pathname,
      privateCluster(pathParams)
        ? { ...pathParams, cluster: PRIVATE_CLUSTER_SENTINEL }
        : pathParams,
    );
  },
  // URLSearchParams encoding (space as `+`), in the caller's key order.
  querySerializer(query) {
    const qs = new URLSearchParams();
    for (const [key, value] of Object.entries(query)) {
      if (value !== undefined && value !== null) qs.set(key, String(value));
    }
    return qs.toString();
  },
});
client.use(kafkitoMiddleware);
