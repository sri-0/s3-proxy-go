# s3-proxy-go

An HTTP proxy for S3-compatible object stores that enforces security-tag based access control on every object, and optionally records all object activity in an Apache Iceberg data catalog.

The proxy sits between clients and one or more S3/MinIO backends. Every object written through it is stamped with a **security tag** carried in a request header. The tag is persisted in a `.meta` JSON sidecar next to the object, and later reads and deletes are only permitted when the caller's JWT contains a role matching that tag. The result is per-object, tag-based access control on top of storage backends that have no concept of it.

```
                        ┌─────────────────────────────────────────┐
                        │                s3-proxy                 │
 client ── JWT+tag ──>  │  routing → op check → tag check → I/O   │ ──> MinIO / S3 account(s)
                        │                   │                     │
                        │                   ├─ <key>.meta sidecar │
                        │                   └─ Iceberg catalog    │ ──> warehouse bucket
                        └─────────────────────────────────────────┘
```

---

## Security architecture

### Trust model

The proxy is designed to run **behind an authenticating gateway** and trusts two things implicitly:

1. **JWTs are not signature-verified.** `internal/auth` parses the bearer token with `ParseUnverified` and extracts the `roles` claim. The assumption is that an upstream gateway (API gateway, service mesh, OIDC proxy) has already authenticated the caller and validated the token. If the proxy is directly reachable, tokens are trivially forgeable — **never expose it without the gateway**.
2. **Backend credentials are held by the proxy, not the client.** Clients never present S3 credentials. The proxy authenticates to each backend account with static keys read from environment variables at startup, so the only path to the data is through the proxy's policy checks.

### Identity and roles

Identity is carried entirely in the JWT `roles` claim, e.g.:

```json
{ "sub": "svc-reporting", "roles": ["C1000", "C1001"] }
```

Roles are opaque strings that are matched byte-for-byte against object security tags. A caller may hold any number of roles. There is no role hierarchy — holding `C1001` grants nothing about `C1000`.

### Security tags

A security tag is a label (e.g. `C1000`, `C1001`) applied to an object at write time and enforced at read/delete time:

- The set of tags the proxy will accept on writes is a static allowlist in config (`allowedSecurityTags`).
- The tag travels in a configurable header, `X-Securitytagid` by default (`securityTagHeader`).
- The tag becomes part of the object's `.meta` sidecar, which is the single source of truth for later authorization decisions.

### The `.meta` sidecar

For every `PUT /prefix/bucket/key`, the proxy writes a second object `key.meta` in the same bucket. It is a flat JSON document built from the request headers:

```json
{
  "securityTagId": "C1001",
  "Content-Type": "application/pdf",
  "X-Correlation-Id": "abc-123"
}
```

- All request headers are captured (canonicalized), **except** those listed in `excludedMetaHeaders` — by default `Authorization`, `Content-Length`, `Host`, and `Connection` — so credentials never land in storage.
- The value of the security tag header is lifted into the well-known `securityTagId` field; everything else is kept as extra metadata.
- Sidecars are hidden from LIST responses.

### Per-operation enforcement

Every request passes up to three gates, in order:

**Gate 1 — routing.** The URL must match a configured account prefix (and bucket, if the account pins its buckets). Unknown paths are 404s; the proxy never forwards blindly.

**Gate 2 — operation allowlist.** Each route carries an allowed-operations set (`LIST`, `GET`, `PUT`, `DELETE`, `HEAD`) resolved from config. A disallowed operation is rejected with `405` before any backend call. This lets you make an account read-only, or a single bucket delete-capable while its siblings are not.

**Gate 3 — tag authorization.** What this means differs per operation:

| Operation | Identity required | Tag check |
|-----------|------------------|-----------|
| `PUT` | none (gap — see below) | Tag header must be present, non-empty, and in `allowedSecurityTags`; all `mandatoryPutHeaders` must be present |
| `GET` | Bearer JWT | Object's sidecar `securityTagId` must be among the caller's roles |
| `DELETE` | Bearer JWT | Same as GET; both object and sidecar are removed |
| `HEAD` | none (gap) | none (gap) |
| `LIST` | none (gap) | none (gap) |

The read path is **fail-closed**: if the sidecar is missing, unreadable, or unparsable, the request is denied with `403` rather than falling back to open access. An object without valid security metadata is unreachable through the proxy.

Write flow in detail (`PUT`):

1. Validate all `mandatoryPutHeaders` are present and non-empty (`400` listing the missing ones otherwise).
2. Validate the security tag against the allowlist (`400` otherwise).
3. Store the object body on the backend.
4. Build and store the `.meta` sidecar. If the sidecar write fails the request fails (`500`), since an object without a sidecar would be unreadable anyway.
5. If the catalog is enabled, record the write. Catalog failures are logged but do not fail the request.

Read flow in detail (`GET`):

1. Extract the bearer token (`401` if missing/malformed) and the `roles` claim (`401` if unparsable).
2. Fetch and parse `key.meta` (`403` on any failure — fail closed).
3. Compare `securityTagId` against roles (`403` on mismatch, with the mismatch logged server-side).
4. Stream the object back with its content type and ETag.

### Catalog security model

When enabled, the catalog mirrors the tag model into the metadata layer. Every column definition carries a `securityLevel`:

- **`base`** — non-sensitive operational metadata (key, bucket, timestamps, content type…).
- **`elevated`** — metadata that should only be visible to holders of the object's tag (the tag itself, custom headers…).

The `tableStrategy` decides how strongly that split is physically enforced:

| Strategy | Layout | Isolation property |
|----------|--------|--------------------|
| `single` | One `object_metadata` table with all columns | Row filtering only — queries are filtered so callers see only rows whose `security_tag_id` is among their roles |
| `two_tier` | A shared `base_metadata` table (base columns) + one `detail_<tag>` table per allowed tag (elevated columns) | Elevated metadata is physically separated per tag; base metadata is visible to any authenticated caller |
| `per_tag` | One `objects_<tag>` table per allowed tag, all columns | Full physical separation — a tag's metadata lives in its own table, useful when tables are also queried by external engines with table-level grants |

Catalog query endpoints require a bearer token with at least one role. Requesting a specific `securityTag` you don't hold is rejected with `403`; otherwise results are scoped to your roles (strategy-dependent, per the table above).

Tables are created at startup for every tag in `allowedSecurityTags`, with partitioning, sort order, and bloom filters taken from the column config.

### Known gaps

Current implementation gaps, called out so they are not mistaken for design intent:

- **PUT is anonymous.** No bearer token is required to write, so writes aren't attributable and an existing object's sidecar can be overwritten — including its tag.
- **Sidecar forgery.** Keys ending in `.meta` are not blocked from direct PUT, so security metadata for another object can be forged by anyone who can write.
- **HEAD and LIST bypass tag checks.** Object existence, size, content type, and key names are visible without any token.
- The catalog's `storage` credentials are not yet passed to the Iceberg warehouse I/O, and the example column config marks `deleted_at` mandatory (non-nullable), which conflicts with live objects having no deletion timestamp. Expect catalog writes to fail until both are resolved.

---

## The Iceberg data catalog

The catalog answers "what objects exist, who tagged them, and what happened to them" without touching the object store — a queryable, permanent inventory of everything written through the proxy, stored in open table format so it can also be read by any Iceberg-aware engine (Trino, Spark, DuckDB, …), not just the proxy's own API.

### Architecture

Two storage planes, deliberately separate from the proxied data:

- **Catalog metadata backend** — tracks table schemas, snapshots, and manifests. Either `sql` (an embedded SQLite database, zero extra infrastructure, single-instance) or `rest` (an Iceberg REST catalog such as Nessie or Polaris, for shared/multi-instance deployments).
- **Warehouse** — an S3 bucket (typically a dedicated MinIO account) holding the actual Parquet data files under `warehousePath`.

Neither plane overlaps with the proxied buckets, so catalog activity never contends with object traffic and the warehouse can have different retention and access rules.

### Record lifecycle

Every catalog row represents the current state of one object, keyed by `(bucket, object_key)`:

- **PUT → upsert.** The proxy first deletes any existing row for the key, then appends a fresh row built from the column config: built-in sources (`_key`, `_bucket`, `_account`, `_timestamp`, …) are filled from the request context, header-sourced columns from the request headers. `created_at`/`updated_at` are set to the write time.
- **DELETE → soft delete.** The row is rewritten with `deleted_at` set rather than removed. The catalog therefore retains a permanent record that the object existed, who could access it (its tag), and when it was removed — an audit trail that survives the object itself. Queries exclude soft-deleted rows unless `includeDeleted=true`.
- Catalog writes happen after the object write succeeds and are **best-effort**: a catalog failure is logged but does not fail the client's request, so the object store remains the source of truth and the catalog is eventually reconcilable.

### Table strategies in depth

The strategy is chosen once (`tableStrategy`) and determines both the physical layout and the isolation guarantee:

**`single`** — everything in one `object_metadata` table carrying all configured columns. Isolation is purely logical: the proxy's query path appends a `security_tag_id IN (<caller roles>)` filter to every scan. Simplest to operate and query, but any external engine granted the table sees all tags' metadata — use only when catalog readers are fully trusted or access is only ever through the proxy API.

**`two_tier`** — splits columns by their `securityLevel`:
- `base_metadata` holds every object's `base` columns, visible to any authenticated caller. This gives organization-wide inventory (what exists, where, when) without exposing sensitive metadata.
- `detail_<tag>` (one per allowed tag) holds the `elevated` columns, plus join keys (`object_key`, `bucket`, `created_at`). Access to a detail table implies holding that tag.

  Proxy queries against a specific tag merge base and detail rows by `bucket:object_key`. Externally, table-level grants on `detail_*` tables map one-to-one onto tags.

**`per_tag`** — one fully independent `objects_<tag>` table per allowed tag, each carrying all columns. No shared table at all: a tag's entire metadata footprint lives in its own table. Proxy queries fan out across the tables matching the caller's roles and concatenate results. This is the strongest isolation and the natural fit when external engines enforce table-level ACLs, at the cost of one table per tag and no cheap cross-tag inventory view.

Tables for every tag in `allowedSecurityTags` are created at startup if missing (namespace included); adding a new tag to config and restarting provisions its tables.

### Physical layout tuning

Column definitions drive Iceberg table features directly, so query performance is configured in the same place as the schema:

- **Partitioning** (`partitionBy`) — any Iceberg transform: `identity`, time-based (`year`/`month`/`day`/`hour` on timestamp columns), `bucket[N]` hashing, or `truncate[N]`. E.g. partitioning `created_at` by `day` and `security_tag_id` by `identity` keeps time-range and tag-scoped scans pruned.
- **Sort order** (`sortOrder: asc|desc`) — contributes the column to the table's write-time sort, improving min/max pruning within files.
- **Bloom filters** (`bloomFilter: true`) — enables Parquet bloom filters on the column for fast point lookups (e.g. `object_key`, `content_type`).
- Tables are created as Iceberg **format v2** with snappy-compressed Parquet.

### Query path

The proxy's `/catalog/*` endpoints translate query params into Iceberg scan filters (equality on `bucket`/`account`/arbitrary columns, prefix match on `object_key`, `deleted_at IS NULL` unless deleted rows are requested) plus the role-based tag scoping described above, then execute a bounded scan (`limit` capped at 1000) and return JSON rows. Because the tables are plain Iceberg, heavier analytics — joins against business data, time-series over `created_at`, storage forensics — belong in an external engine pointed at the same catalog and warehouse.

---

## Configuration

Config is a single YAML file, resolved in this order: the `-config <path>` flag, the `CONFIG_FILE` environment variable, then `./config.yaml`. Invalid config is rejected at startup with a specific error. See [`config.example.yaml`](config.example.yaml) for a complete annotated example.

### Top-level

| Key | Default | Description |
|-----|---------|-------------|
| `listenAddr` | `:8080` | Listen address |
| `allowedSecurityTags` | — (required, ≥1) | Tag IDs accepted on PUT and used to create per-tag catalog tables |
| `securityTagHeader` | `X-Securitytagid` | Header carrying the tag |
| `mandatoryPutHeaders` | `[<securityTagHeader>]` | Headers that must be present and non-empty on every PUT |
| `excludedMetaHeaders` | `Authorization`, `Content-Length`, `Host`, `Connection` | Headers omitted from sidecars |
| `accounts` | — (required, ≥1) | Backend S3 accounts (below) |
| `catalog` | disabled | Iceberg catalog (below) |

### Accounts and routing

Each entry in `accounts` maps one backend endpoint to a URL prefix:

```yaml
accounts:
  - name: primary                   # unique; used in logs and the catalog
    endpoint: minio1:9000
    region: us-east-1
    accessKeyEnvVar: PRIMARY_ACCESS_KEY   # env var *names* — secrets never live in config
    secretKeyEnvVar: PRIMARY_SECRET_KEY
    pathPrefix: /primary            # URL prefix this account is served under
    useSSL: false
    allowedOperations: [LIST, GET, PUT, HEAD]   # account-wide default
    buckets:                        # optional — omit to serve any bucket name
      - name: documents
        allowedOperations: [LIST, GET, PUT, DELETE, HEAD]  # overrides account ops
      - name: images                # inherits account ops
```

Routing and resolution rules:

- With a `buckets` list, only those buckets are routed: `PUT /primary/documents/reports/q3.pdf` → bucket `documents`, key `reports/q3.pdf`. Anything else under the prefix is a 404.
- Without one, the first path segment after the prefix is treated as the bucket name (wildcard account).
- **Operation precedence:** a bucket's `allowedOperations` completely replaces the account's for that bucket; if the bucket omits it, the account's applies; if the account also omits it, all five operations are allowed. Operation names are case-insensitive in config and must be one of `LIST`, `GET`, `PUT`, `DELETE`, `HEAD`.
- Credentials are resolved from the named env vars at startup; the process refuses to start if either is unset.
- Validation rejects duplicate account names and **route clashes** — two accounts claiming the same `pathPrefix`+bucket pair, or two wildcard accounts on one prefix.

### Catalog

```yaml
catalog:
  enabled: true

  catalogType: sql                  # 'sql' (SQLite-backed) or 'rest'
  sqlitePath: "./catalog.db"        # required for sql
  # catalogURI: "http://nessie:19120/api/v1"   # required for rest
  catalogNamespace: "s3proxy"       # Iceberg namespace for all tables

  storage:                          # dedicated warehouse for Parquet data files
    endpoint: minio-catalog:9000
    region: us-east-1
    bucket: iceberg-warehouse
    accessKeyEnvVar: CATALOG_ACCESS_KEY
    secretKeyEnvVar: CATALOG_SECRET_KEY
    useSSL: false
    warehousePath: s3://iceberg-warehouse/metadata-catalog

  tableStrategy: two_tier           # single | two_tier | per_tag
  failOnError: false

  columns:
    mandatory:                      # non-nullable; every row must have a value
      - name: object_key
        source: _key
        icebergType: string
        securityLevel: base
        sortOrder: asc
      - name: security_tag_id
        source: X-Securitytagid
        icebergType: string
        securityLevel: elevated
        partitionBy: identity
        bloomFilter: true
      - name: created_at
        source: _timestamp
        icebergType: timestamptz
        securityLevel: base
        partitionBy: day
        sortOrder: desc
    optional: []                    # nullable columns, same shape
```

Each column definition:

| Field | Values | Description |
|-------|--------|-------------|
| `name` | — | Column name in the Iceberg table (must be unique) |
| `source` | header name or built-in | Where the value comes from on each write (below) |
| `icebergType` | `string`, `long`, `int`, `boolean`, `float`, `double`, `timestamptz`, `date`, `binary`, `uuid` | Iceberg column type |
| `securityLevel` | `base`, `elevated` | Which tier the column belongs to (see catalog security model) |
| `partitionBy` | `identity`, `year`, `month`, `day`, `hour`, `bucket[N]`, `truncate[N]` | Optional partition transform |
| `sortOrder` | `asc`, `desc` | Optional table sort order contribution |
| `bloomFilter` | bool | Enables a Parquet bloom filter on the column |

Built-in `source` values (anything else is looked up as a request header):

| Source | Value |
|--------|-------|
| `_key` | Object key |
| `_bucket` | Bucket name |
| `_account` | Account name |
| `_timestamp` | Write time (UTC) |
| `_timestamp_updated` | Last update time |
| `_timestamp_deleted` | Deletion time (null while the object is live — so it must be an *optional* column) |

Deletes are recorded as soft deletes: the row is re-written with `deleted_at` set, so the catalog retains a permanent record that the object existed.

---

## HTTP API

### Proxy endpoints

| Method & path | Description | Success | Errors |
|---------------|-------------|---------|--------|
| `PUT /{prefix}/{bucket}/{key}` | Write object + sidecar | `200` | `400` missing headers / bad tag, `405` op not allowed, `502` backend |
| `GET /{prefix}/{bucket}/{key}` | Read object | `200` | `401` no/bad token, `403` tag mismatch or missing sidecar, `404`, `405`, `502` |
| `DELETE /{prefix}/{bucket}/{key}` | Delete object + sidecar | `204` | same as GET |
| `HEAD /{prefix}/{bucket}/{key}` | Object size / content-type / ETag | `200` | `404`, `405`, `502` |
| `GET /{prefix}/{bucket}/` | List objects (S3 `ListBucketResult` XML, `?prefix=` supported, sidecars hidden) | `200` | `405`, `502` |

### Catalog endpoints (bearer token with roles required)

| Method & path | Description |
|---------------|-------------|
| `GET /catalog/objects` | Query entries. Params: `bucket`, `keyPrefix`, `securityTag`, `account`, `includeDeleted`, `limit` (default 100, max 1000) — plus any column name as an equality filter |
| `GET /catalog/objects/{bucket}/{key}` | Full history for one object, deleted entries included |
| `GET /catalog/stats` | Object counts grouped by content type and account (`bucket`/`account` params) |

---

## Running

```sh
go build ./cmd/s3-proxy

PRIMARY_ACCESS_KEY=... PRIMARY_SECRET_KEY=... \
  ./s3-proxy -config config.yaml
```

```sh
# Write (tag must be allowlisted)
curl -X PUT http://localhost:8080/primary/documents/report.pdf \
  -H "X-Securitytagid: C1001" \
  --data-binary @report.pdf

# Read back — the JWT's roles must include C1001
curl http://localhost:8080/primary/documents/report.pdf \
  -H "Authorization: Bearer $JWT"

# List
curl "http://localhost:8080/primary/documents/?prefix=reports/"

# Catalog query
curl "http://localhost:8080/catalog/objects?bucket=documents&keyPrefix=reports/" \
  -H "Authorization: Bearer $JWT"
```

## Development

```sh
go build ./...
go vet ./...
go test ./...
```

| Package | Purpose |
|---------|---------|
| `cmd/s3-proxy` | Entrypoint: config load, backend + catalog init, HTTP server |
| `internal/proxy` | Router, route config, and GET/PUT/DELETE/HEAD/LIST handlers |
| `internal/auth` | Bearer token extraction, JWT role parsing, tag validation |
| `internal/config` | YAML loading, defaults, validation (ops, route clashes) |
| `internal/meta` | `.meta` sidecar build/parse |
| `internal/backend` | `Backend` interface, MinIO implementation, in-memory fake for tests |
| `internal/catalog` | Iceberg catalog: config, schema/partition/sort building, writer, reader, query handlers |

Tests cover the proxy handlers end-to-end against the in-memory backend, plus config validation, auth, and sidecar building. The catalog package currently has no test coverage.
