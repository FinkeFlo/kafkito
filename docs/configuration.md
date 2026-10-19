# Configuration

kafkito reads an optional YAML file (`--config` or `KAFKITO_CONFIG`) and
`KAFKITO_*` environment variables. Later sources win:

```text
built-in defaults → YAML file → KAFKITO_* variables → $PORT
```

Every setting is checked at startup; an invalid value logs the reason and
exits with code 2. An invalid listen address (for example `PORT=abc`) fails
the same way.

Per-cluster data masking rules (`clusters[].data_masking`, for record values,
keys and headers) are described in [Data Masking](data-masking.md).

## Settings

Each setting names the environment variable, then its YAML key and default.

### Server

- **`KAFKITO_SERVER_ADDR`** (YAML `server.addr`, default `:37421`)  
  Listen address.
- **`PORT`**  
  If set and non-empty, overrides `server.addr` with `:$PORT` (Cloud Foundry, Heroku). Environment only.
- **`KAFKITO_TEST_CONNECTION_TIMEOUT`** (YAML `server.test_connection_timeout`, default `15s`)  
  Go duration (`30s`, `2m`) for the private-cluster **Test connection** probe. `0` means the default; invalid or negative values fail startup.
- **`KAFKITO_SERVER_FRAME_ANCESTORS`** (YAML `server.frame_ancestors`, default `'none'`)  
  CSP `frame-ancestors` source list, see [Security headers](#security-headers).

### Clusters

- **`KAFKITO_KAFKA_BROKERS`**  
  Shortcut when no `clusters` are configured: defines one cluster named `local` from a comma-separated broker list. Environment only.
- **`KAFKITO_PRIVATE_CLUSTERS`** (YAML `private_clusters.mode`, default `on`)  
  Who may use [private clusters](architecture.md#private-clusters): `on` (every user), `off` (nobody) or `role` (RBAC subjects with the permission `private_cluster:use`; needs `rbac.enabled`). Case-insensitive; a YAML boolean counts as `on`/`off`. Other values fail startup.
- **`KAFKITO_PRIVATE_CLUSTERS_ALLOW_PLAIN_WITHOUT_TLS`** (YAML `private_clusters.allow_plain_without_tls`, default `false`)  
  `true` lets private clusters use SASL/PLAIN without TLS, which sends the password in clear text; by default such a definition gets `400 plain_without_tls`. Meant for local development brokers. Configured clusters are not affected; kafkito logs a startup warning for each configured cluster with SASL/PLAIN without TLS or `insecure_skip_verify`. Values other than a boolean fail startup.

### Authentication

- **`KAFKITO_AUTH_MODE`** (YAML `auth.mode`, default `off`)  
  `oidc` or `mock` (every build), `xsuaa` (`-btp` build) or `off` (only `-tags devauth` builds such as the `-local` image). A mode the build cannot serve fails startup, so the default and `-btp` builds need this set. See [Auth modes](#auth-modes).
- **`KAFKITO_AUTH_OIDC_ISSUER_URL`** (YAML `auth.oidc.issuer_url`)  
  Mode `oidc`, required: the exact `iss` of accepted tokens and the base for discovery. `https` (plain `http` only on a loopback host), no query or fragment.
- **`KAFKITO_AUTH_OIDC_AUDIENCE`** (YAML `auth.oidc.audience`)  
  Mode `oidc`, required: a value the token's `aud` must contain. Use the identifier of the kafkito API, not the client ID of the auth proxy.
- **`KAFKITO_AUTH_OIDC_JWKS_URL`** (YAML `auth.oidc.jwks_url`)  
  Mode `oidc`: the signing-key (JWKS) URL. Empty means discovery at startup. Same URL rule as the issuer.
- **`KAFKITO_AUTH_OIDC_REQUIRED_TYP`** (YAML `auth.oidc.required_typ`)  
  Mode `oidc`: when set (for example `at+jwt`), tokens must carry this `typ` header.
- **`KAFKITO_AUTH_OIDC_ALLOWED_AZP`** (YAML `auth.oidc.allowed_azp`)  
  Mode `oidc`: when set, a comma-separated list (YAML: a list) of accepted `azp` values; tokens without `azp` are rejected.
- **`KAFKITO_INSECURE_AUTH_OFF`**  
  `true` allows mode `off` on a non-loopback address. Ignored on Cloud Foundry, where `off` always fails startup. Environment only.

### Log level and format

- **`KAFKITO_LOG_LEVEL`** (YAML `log.level`, default `info`)  
  `debug`, `info`, `warn` or `error`.
- **`KAFKITO_LOG_FORMAT`** (YAML `log.format`, default `json`)  
  `json` or `text`; `make dev` uses `text`.

## Auth modes

`KAFKITO_AUTH_MODE` (YAML `auth.mode`, case-sensitive, default `off`) picks
how kafkito checks the bearer token on `/api/v1/*`; `/healthz` and `/readyz`
are never authenticated. kafkito builds the validator before it listens and
then logs `auth initialised` with the mode. An unknown mode, or one the build
cannot serve, logs `auth init failed` at `error` and exits with code 2.

kafkito has no login flow of its own: an auth proxy in front of it (for
example oauth2-proxy, or the SAP approuter) handles the login and forwards
the bearer token.

| Mode | Builds | Use it for |
|---|---|---|
| `off` | `-tags devauth` builds (`-local` image) | Local evaluation and development |
| `mock` | every build | Smoke-testing an image; every API request gets `401` |
| `oidc` | every build | Any OpenID Connect identity provider |
| `xsuaa` | `-btp` builds | SAP BTP with an XSUAA service binding |

### `off`

Every request gets the synthetic principal `dev-user`; no IdP is involved.
Other builds exit with code 2. Startup also exits with code 2
(`insecure auth configuration`) always on Cloud Foundry (`VCAP_APPLICATION`
set), and on a non-loopback address unless `KAFKITO_INSECURE_AUTH_OFF=true`.

### `mock`

Starts an issuer on a random `127.0.0.1` port with a signing key created at
startup and loads its keys. No client can get a token, so every API request
gets `401`. The issuer runs inside the process, so there is no external IdP
that could fail.

### `oidc`

**Startup.** Needs `KAFKITO_AUTH_OIDC_ISSUER_URL` and
`KAFKITO_AUTH_OIDC_AUDIENCE`; a missing value, or an issuer or JWKS URL that
is not `https` (plain `http` only on a loopback host), exits with code 2.

Without `KAFKITO_AUTH_OIDC_JWKS_URL`, kafkito reads
`<issuer>/.well-known/openid-configuration` (10 s timeout, 1 MB limit, at
most 5 redirects, none from `https` to `http`, each target following the same
URL rule). Its `issuer` must equal the configured issuer exactly and its
`jwks_uri` must follow the same URL rule (and be `https` when the issuer is).
If that fails, kafkito logs `auth init failed` and exits with code 2.

Then it loads the keys, waiting at most 5 s, and logs `auth initialised`
with issuer, audience and JWKS URL.

**IdP outage.** At startup, failed discovery exits with code 2 (see above).
When the keys do not load (with `KAFKITO_AUTH_OIDC_JWKS_URL` set, or after a
successful discovery), kafkito logs `auth: JWKS warm-up failed; startup
continues` at `warn` and serves; API requests get `401` until the keys load,
and `/readyz` does not change. Later, requests keep working with the keys
already loaded; a token signed with a new key gets `401` until the IdP
answers again.

### `xsuaa`

**Startup.** Reads the first `xsuaa` binding from `VCAP_SERVICES`. A missing
variable or binding, invalid JSON, or a binding without `url`, `uaadomain` or
`xsappname` (or with a scheme or port in `uaadomain`) exits with code 2
(`auth init failed`). Then it loads the keys from `<url>/token_keys`,
waiting at most 5 s.

**IdP outage.** At startup, kafkito logs `auth: JWKS warm-up failed; startup
continues` at `warn` (also when `<url>/token_keys` breaks the `jku` rules
below) and serves; API requests get `401` until the keys load, and `/readyz`
does not change. Later, requests keep working with the keys already loaded;
a token signed with a new key, or naming a `jku` first seen during the
outage, gets `401` until the IdP answers again.

See [ADR-0004](adr/0004-xsuaa-build-tag.md) for the build-tag rationale.

### Key loading

How `mock`, `oidc` and `xsuaa` load keys:

- One load per key URL runs at a time and all requests share it. A request
  waits at most 5 s for it and then gets `401`; the load itself may take up
  to 10 s, so a slow IdP still fills the cache for later requests.
- A load starts at most once per minute per URL: for the first keys, when a
  token names a `kid` the cached keys lack (key rotation), or in the
  background on the first request after the keys are 15 min old. Once that
  load has ended, requests in the rest of the minute do not wait: without
  keys they get `401` at once, and a token with an unknown `kid` is checked
  against the cached keys and rejected.
- Nothing retries on a timer: after a failed start the next load begins with
  the first API request at least a minute later.
- A failed load logs `jwks fetch failed` at `warn` and keeps the previous
  keys. The URL is logged for the configured key URL only; key URLs taken
  from a token (`xsuaa`) are not.
- A rejected token is logged only at `debug` (`auth validate failed`, with
  the rule that failed and no claim values); the `401` itself appears in the
  request log at `info`.

### Token rules

Tokens (`mock`, `oidc` and `xsuaa`) must meet all of these:

- A compact JWS with exactly one signature, a `kid` header and a JSON claim
  set as payload.
- Header `alg` is one of `RS256`, `RS384`, `RS512`, `PS256`, `PS384`,
  `PS512`, `ES256`, `ES384`, `ES512` or `EdDSA` (HMAC and `none` are always
  rejected) and equals the key's `alg` when the key has one.
- `exp` is present and not past; `nbf` and `iat`, when present, are checked
  too, with 60 s clock skew allowed.
- `sub` is a non-empty string and `aud` is present.
- `mock`: `iss` equals the embedded issuer URL exactly, `aud` contains
  `mock-client`.
- `oidc`: `iss` equals `KAFKITO_AUTH_OIDC_ISSUER_URL` exactly (no
  normalization, so a trailing slash counts), and `aud` contains
  `KAFKITO_AUTH_OIDC_AUDIENCE`. With `KAFKITO_AUTH_OIDC_REQUIRED_TYP` set,
  the `typ` header must match it (case-insensitive, an `application/` prefix
  is ignored); with `KAFKITO_AUTH_OIDC_ALLOWED_AZP` set, `azp` must be one of
  the listed values. Both are off by default. Set the audience to the
  identifier of the kafkito API rather than the client ID of the auth proxy:
  many IdPs put the client ID into the `aud` of ID tokens, and `required_typ`
  (`at+jwt` for RFC 9068 access tokens) keeps ID tokens out where the IdP
  sets `typ`. The RBAC identity is `user_name`, else `preferred_username`,
  else `sub`; check that your IdP does not let users change the claim you
  map RBAC subjects to.
- `xsuaa`: `iss` is the binding `url` or a path below it, `aud` contains the
  binding `clientid` or `xsappname`, `zid` equals the binding
  `identityzoneid` when that is set, and the `jku` header is
  `https://<uaadomain or a subdomain>/token_keys` (a DNS name, no port other
  than 443, no user info, query or fragment). At most 16 different `jku`
  URLs, the binding's own included, are accepted per process; tokens naming
  further ones get `401`.

## Logging

kafkito logs to stdout via `log/slog`, one line per event; level and format
are set under [Settings → Log level and format](#log-level-and-format).

Every API request gets a request id (from `X-Vcap-Request-Id`,
`traceparent` or `X-Request-Id`, else generated), echoed in the
`X-Request-Id` response header and attached to its log lines. Server errors
(5xx) log at `warn`, client errors (4xx) and requests slower than 2s at
`info`, all other requests at `debug`; health probes and static assets are not
logged, and query strings, headers and bodies never are.

Warnings and errors can name the broker and Schema Registry addresses of the
clusters involved, private clusters included (see
[Private clusters](architecture.md#private-clusters)); restrict who can
read the logs accordingly.

To see every request on SAP BTP Cloud Foundry temporarily:

```sh
cf set-env <app> KAFKITO_LOG_LEVEL=debug && cf restart <app>
```

## Security headers

Every response (API, UI and static files) carries:

```text
Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'
X-Content-Type-Options: nosniff
Referrer-Policy: no-referrer
Cross-Origin-Opener-Policy: same-origin
X-Frame-Options: DENY
```

Private-cluster credentials are kept in the browser (remembered passwords in
localStorage, the others in the open tab), so the policy allows no inline
scripts or styles and no third-party origins.

API responses (`/api/...`, errors and the copy event stream included) also
carry `Cache-Control: no-store`, so neither the browser nor a proxy keeps a
copy of cluster data. The UI keeps its own caching: hashed files under
`/assets/` are cached for a year, `index.html` is revalidated (`no-cache`).

- **Embedding in an iframe** (e.g. an SAP BTP launchpad): set
  `KAFKITO_SERVER_FRAME_ANCESTORS` (YAML `server.frame_ancestors`) to a
  space-separated CSP source list, e.g. `'self' https://*.launchpad.example.com`.
  `X-Frame-Options: DENY` is only sent while the value is `'none'`, because it
  cannot express an allow-list.
- **HSTS** is not set by kafkito: TLS is terminated by the upstream proxy or
  router, which should send `Strict-Transport-Security` (see
  [Running behind TLS](architecture.md#running-behind-tls)).
- **`make dev`** serves the UI through Vite, which sets none of these headers;
  the policy only applies when the Go binary serves the UI (`make build`,
  images, `make e2e`).
