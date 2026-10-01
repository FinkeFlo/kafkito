# Private-cluster import bundles

Drop-in JSON files you can import into Kafkito via **Settings → Clusters → Import JSON**.
Bundles use the canonical `kafkito.private-clusters/v1` schema; the importer validates
each cluster entry and dedups by `id`, so re-importing the same file updates the existing
row instead of creating a duplicate.

## What's in here

| File | Contents | When to use |
| --- | --- | --- |
| [`localhost-dev.json`](localhost-dev.json) | Single cluster pointing at `localhost:39092` plain + Schema Registry on `localhost:38081` | When running the project's `docker compose up -d` dev stack alongside Kafkito |
| [`localhost-secure.json`](localhost-secure.json) | Single cluster, SASL/PLAIN without TLS on `localhost:39093` with the demo user `kafkito / kafkito-secret` | When the `kafka-secure` profile of the dev stack is up and the server allows SASL/PLAIN without TLS (see [Server settings](#server-settings)) |
| [`confluent-cloud-template.json`](confluent-cloud-template.json) | TLS + SASL/PLAIN template with `REPLACE_ME` placeholders | Starting point for a Confluent Cloud cluster — fill broker hostname, API key, API secret, and the Schema Registry URL/keys |
| [`multi-env-bundle.json`](multi-env-bundle.json) | Three clusters in one bundle (dev / staging / prod), SCRAM-SHA-512, TLS, SR per env | Onboarding a new colleague: ship them one file with all relevant environments |

## How an import works

1. Open Kafkito → **Settings** → **Clusters**
2. Click **Import JSON**, pick a file from this folder
3. Toast shows `Imported: N added, 0 updated, 0 skipped`, with a tip that the file was not
   encrypted
4. The cluster appears in the table; the credentials never leave your browser

The bundle file is read once at import time and converted into entries in `localStorage`
under key `kafkito.private-clusters.v1`. Subsequent requests against that cluster send the
credentials in the `X-Kafkito-Cluster` header — the server itself stays stateless.

Files written by **Export JSON** are encrypted with a passphrase. Importing one asks for
that passphrase first; the decrypted content is a bundle in the shape below.

## Server settings

The server decides whether these clusters can be used:

- Operators can disable private clusters or restrict them to a role with
  `private_clusters.mode` (env `KAFKITO_PRIVATE_CLUSTERS`): `on` (the default) allows
  them for every user, `off` for nobody, and `role` only for RBAC subjects with the
  permission `private_cluster:use`. When your account may not use private clusters,
  **Import JSON** is disabled.
- kafkito rejects SASL/PLAIN without TLS for private clusters with
  `400 plain_without_tls`, because PLAIN sends the password in clear text.
  `localhost-secure.json` uses SASL/PLAIN without TLS, so it only works when the
  operator sets `private_clusters.allow_plain_without_tls: true` (env
  `KAFKITO_PRIVATE_CLUSTERS_ALLOW_PLAIN_WITHOUT_TLS=true`). Set it on a local
  development server only. The other bundles use TLS or no authentication.

## Editing an example before importing

The placeholder bundles (`confluent-cloud-template.json`, `multi-env-bundle.json`) ship
with `REPLACE_ME` strings. Two ways to fill them in:

1. Edit the JSON in your text editor, then import the saved file
2. Import as-is, then click the pencil icon next to the row to open the edit form (the
   placeholder values become editable inputs)

The cluster `id` field can stay as-is — UUIDs are unique enough that two colleagues with
the same template bundle will not collide as long as you both keep the IDs that ship
with the file.

## Shape reference

```jsonc
{
  "schema": "kafkito.private-clusters/v1",      // required, exact match
  "exported_at": "<ISO 8601 timestamp>",        // informational
  "clusters": [
    {
      "id": "<uuid>",                           // required, dedup key on import
      "name": "<display name>",                 // required
      "brokers": ["host:port", ...],            // required, at least one
      "auth": {                                 // required (use type: "none" for unauth)
        "type": "none" | "plain" | "scram-sha-256" | "scram-sha-512",
        "username": "<...>",                    // omit when type is "none"
        "password": "<...>"                     // omit when type is "none"
      },
      "tls": {
        "enabled": true | false,                // required
        "insecure_skip_verify": true | false    // optional, default false
      },
      "schema_registry": {                      // optional, omit if not used
        "url": "<https://...>",
        "username": "<...>",                    // optional (omit for unauth SR)
        "password": "<...>",                    // optional
        "insecure_skip_verify": true | false    // optional
      },
      "created_at": <unix-millis>,              // optional but recommended
      "updated_at": <unix-millis>               // optional but recommended
    }
  ]
}
```

## Security note

These bundles store passwords and API secrets in **plaintext**. Treat them like any other
credential file: keep them out of git, share via a password manager or encrypted channel
(gpg, age, 1Password attachment), and rotate the secrets in the source-of-truth (your IdP,
Confluent Cloud console, etc.) when the file is no longer needed. Once imported, **Export
JSON** writes an encrypted copy (passphrase of at least 12 characters, AES-256-GCM); keep
that instead of a plaintext bundle with real secrets.

The browser side has the same constraint — `localStorage` stores them in plaintext too.
That trade-off is documented in `frontend/src/lib/private-clusters.ts`.
