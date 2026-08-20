# s3-proxy-go

An HTTP proxy for S3-compatible object stores that adds security-tag based access control and an optional Apache Iceberg data catalog that tracks every object written through it.

The proxy sits between clients and one or more S3/MinIO backends. Every object written through it gets a `.meta` JSON sidecar capturing the request headers, including a mandatory security tag. Reads and deletes are only allowed when the caller's JWT roles include the object's security tag.

## How it works

```
client ──> s3-proxy ──> MinIO / S3 backend(s)
              │
              ├── .meta sidecar per object (security tag + headers)
              └── optional Iceberg catalog (object metadata tables)
```

- **PUT** — requires the security tag header (`X-Securitytagid` by default) with a value from the configured allowlist, plus any other mandatory headers. Stores the object and a `<key>.meta` sidecar built from the request headers. Records the write in the catalog if enabled.
- **GET / DELETE** — requires `Authorization: Bearer <JWT>`. The proxy reads the object's `.meta` sidecar and only proceeds if the token's `roles` claim contains the object's security tag.
- **HEAD** — returns object size, content type, and ETag.
- **LIST** — returns an S3-style `ListBucketResult` XML document; `.meta` sidecars are filtered out. Supports a `prefix` query parameter.

Operations can be allowed or denied per account and per bucket via config.

### Security model

The proxy **does not verify JWT signatures**. It assumes an upstream gateway has already authenticated the request and validated the token; the proxy only extracts the `roles` claim. It must therefore never be reachable except through that gateway.

Known gaps in the current implementation (see git history / open work):

- PUT does not require a bearer token, so writes are not tied to an identity and an existing object's sidecar can be overwritten.
- Keys ending in `.meta` are not blocked from direct PUT.
- HEAD and LIST do not check the caller's roles against object security tags.

## Configuration

Configuration is YAML, loaded from `-config <path>`, the `CONFIG_FILE` env var, or `./config.yaml` (in that order). See [`config.example.yaml`](config.example.yaml) for a full example.

Top-level settings:

| Key | Description |
|-----|-------------|
| `listenAddr` | Address to listen on (default `:8080`) |
| `allowedSecurityTags` | Security tag IDs accepted on PUT (e.g. `C1000`) |
| `securityTagHeader` | Header carrying the tag (default `X-Securitytagid`) |
| `mandatoryPutHeaders` | Headers required on every PUT |
| `excludedMetaHeaders` | Headers omitted from `.meta` sidecars (e.g. `Authorization`) |
| `accounts` | One entry per backend S3 account |
| `catalog` | Optional Iceberg catalog settings |

Each account has an `endpoint`, a `pathPrefix` the proxy serves it under, env var names for its credentials (`accessKeyEnvVar` / `secretKeyEnvVar` — the keys themselves are never stored in config), an `allowedOperations` list, and optionally a fixed set of `buckets` with per-bucket operation overrides. An account without a `buckets` list serves any bucket name under its prefix.

## Iceberg catalog (optional)

When `catalog.enabled` is true, every PUT and DELETE is recorded in Apache Iceberg tables (Parquet on S3, tracked by a SQLite or REST catalog). Columns are fully config-driven: each column maps to a request header or a built-in source (`_key`, `_bucket`, `_account`, `_timestamp`, `_timestamp_deleted`, …) with an Iceberg type, optional partitioning, sort order, and bloom filters.

Three table strategies:

- `single` — one `object_metadata` table for everything.
- `two_tier` — a `base_metadata` table with base-level columns, plus a `detail_<tag>` table per security tag holding elevated columns.
- `per_tag` — a fully separate `objects_<tag>` table per security tag.

Query endpoints (all require a bearer token with roles):

| Endpoint | Description |
|----------|-------------|
| `GET /catalog/objects` | Query entries; filters: `bucket`, `keyPrefix`, `securityTag`, `account`, `includeDeleted`, `limit`, plus any column name |
| `GET /catalog/objects/{bucket}/{key}` | History for one object (includes deleted entries) |
| `GET /catalog/stats` | Object counts grouped by content type and account |

> **Status:** the catalog is new and not yet exercised by tests. The `catalog.storage` credentials are not yet passed through to the Iceberg warehouse I/O, and the example column config marks `deleted_at` mandatory (non-nullable) which conflicts with live objects having no deletion timestamp — expect writes to fail until these are resolved.

## Running

```sh
go build ./cmd/s3-proxy

PRIMARY_ACCESS_KEY=... PRIMARY_SECRET_KEY=... \
  ./s3-proxy -config config.yaml
```

Example requests:

```sh
# Write an object (tag must be in allowedSecurityTags)
curl -X PUT http://localhost:8080/primary/documents/report.pdf \
  -H "X-Securitytagid: C1001" \
  --data-binary @report.pdf

# Read it back (JWT roles must include C1001)
curl http://localhost:8080/primary/documents/report.pdf \
  -H "Authorization: Bearer $JWT"

# List
curl "http://localhost:8080/primary/documents/?prefix=reports/"
```

## Development

```sh
go build ./...   # build
go vet ./...     # vet
go test ./...    # run tests
```

Package layout:

| Package | Purpose |
|---------|---------|
| `cmd/s3-proxy` | Entrypoint: config load, backend + catalog init, HTTP server |
| `internal/proxy` | Router and GET/PUT/DELETE/HEAD/LIST handlers |
| `internal/auth` | Bearer token extraction, JWT role parsing, tag validation |
| `internal/config` | YAML config loading and validation |
| `internal/meta` | `.meta` sidecar build/parse |
| `internal/backend` | `Backend` interface, MinIO implementation, in-memory fake for tests |
| `internal/catalog` | Iceberg catalog: schema building, writer, reader, query HTTP handlers |
