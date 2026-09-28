# Data masking

kafkito can redact parts of Kafka records before they reach a user. Rules are
configured per cluster under `clusters[].data_masking` in the YAML config and
apply to every path that returns or derives from record data: consume, the
path-picker sample, search, raw download, replay and topic copy.

## Rule reference

```yaml
clusters:
  - name: prod
    brokers: ["broker-1:9092"]
    data_masking:
      # Value only (no targets): exactly the behaviour before targets existed.
      - topics: ["^orders$", "^user-.*"]
        fields: ["$.email", "$.customer.phone"]
        replacement: "***"
      # Customer ids in the key, JSON or plain text.
      - topics: ["^orders$"]
        targets: [key]
        fields: ["$.customer_id"]
        regex:
          - match: "cust-[0-9]+"
            replacement: "cust-***"
      # Tokens in the authorization and x-api-* headers.
      - topics: ["^orders$"]
        targets: [headers]
        headers: ["(?i)^authorization$", "(?i)^x-api-"]
        regex:
          - match: ".+"
            replacement: "[redacted]"
```

| Key           | Type                  | Meaning |
| ------------- | --------------------- | ------- |
| `topics`      | list of Go regex      | Topics the rule applies to; a rule without `topics` applies to every topic. Patterns are not anchored — use `^…$` for an exact name. |
| `targets`     | list of `value`, `key`, `headers` | Record parts the rule masks. Omitted or empty = `[value]`, so rules written before targets existed behave exactly as before. |
| `headers`     | list of Go regex      | With the `headers` target: only header **keys** matching one of the patterns have their values masked. Omitted = every header. Setting it without the `headers` target is a config error. Header keys themselves are never masked. |
| `fields`      | list of JSONPath      | Replaced with `replacement` when the part is valid JSON (the decoded JSON for Schema Registry payloads). |
| `regex`       | list of `{match, replacement}` | Go regex substitutions on the string form of the part, applied after `fields`. An empty `replacement` means `***`. |
| `replacement` | string                | Replacement for `fields`; empty = `***`. |

Binary parts are masked on their `0x…` hex rendering, the same text the UI
shows. All rules matching a topic and a part apply: first the `fields` of
every such rule, then their `regex` substitutions, each in config order.

## Validation

The rules are validated at startup. An unknown target, a `headers` selector
without the `headers` target, or a malformed topic/header pattern, JSONPath or
regex fails startup with an error naming the rule, for example:

```text
clusters[0] (prod): data_masking[1]: targets: unknown target "body" (use value|key|headers)
```

## Behaviour per path

| Path | Masked value | Masked key | Masked header value |
| ---- | ------------ | ---------- | ------------------- |
| Consume (`GET .../messages`) and sample (`GET .../sample`) | Masked on the full decoded value before the 64 KB preview cut, `masked: true`, `value_b64` omitted | Masked, `key_masked: true`, `key_b64` omitted | Masked, header key listed in `masked_headers`, its `headers_b64` entry omitted |
| Search (`POST .../messages/search`) | Matched in masked form (all modes) | Matched in masked form (`contains` key zone, `js` `key`) | Matched in masked form (`contains` headers zone, `js` `headers`) |
| Search parse errors | Details withheld on topics with any rule | same | same |
| Raw download (`GET .../raw`) | `403 value_masked` | Allowed — the body is the value only | Allowed — the body is the value only |
| Topic copy (`POST .../copy`) | Record skipped (`skipped`) | Record skipped | Record skipped |
| Replay (UI) | Blocked with the reason shown | Blocked with the reason shown | Blocked with the reason shown |
| PathSense suggestions (UI) | Built from the masked rendering; truncated masked values are not hydrated | Not used | Not used |
| Count and timeline | Offsets only, no record data | same | same |

The UI marks masked parts with a crossed-out eye and the word *masked*
(`masked`, `key masked`, `headers masked`, and per header line in the
detail view).

## Private clusters

Private clusters (the `X-Kafkito-Cluster` header) never carry masking: the
user brings their own credentials and sees the raw data. A `data_masking`
list inside a private cluster config is ignored.
