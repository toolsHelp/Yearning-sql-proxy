# sql-relay — Read-Only SQL Relay Proxy

[![CI](https://github.com/toolsHelp/Yearning-sql-proxy/actions/workflows/ci.yml/badge.svg)](https://github.com/toolsHelp/Yearning-sql-proxy/actions/workflows/ci.yml)

Program name: `sql-relay` (repository name: Yearning-sql-proxy — both refer to the same project).
中文版: [README.md](README.md)

> **Disclaimer**
> This is an unofficial, third-party read-only query proxy for
> [Yearning](https://github.com/cookieY/Yearning). It is not an official Yearning project,
> fork, or release, and is not endorsed by its maintainers.
> MySQL is a registered trademark of Oracle Corporation and/or its affiliates. This project
> only implements the MySQL wire protocol to interoperate with standard MySQL clients; it is
> not affiliated with, authorized, or endorsed by Oracle.

It impersonates a MySQL 5.7 server so that standard MySQL clients such as DataGrip and
Navicat can connect directly. Read-only queries are forwarded to the **Yearning backend**
for execution, and results are returned over the MySQL protocol.

The real database credentials always stay on the Yearning server. The proxy only keeps your
Yearning login password locally and never writes any database password to disk.

> **Just want to use it?** If you received a prebuilt binary and simply want to query data from
> DataGrip, read the **[User Guide](USAGE_EN.md)** (setup → start → connect → FAQ;
> Chinese version: [使用说明.md](使用说明.md)). The rest of this file targets building and
> development.

## Background (Why this exists)

In many companies, Yearning serves as the **database approval platform**: all production
DDL/DML goes through ticket-based review, and real database credentials are held centrally
by the platform so developers never touch them directly. That model is valuable for security
and compliance, but for **day-to-day querying and debugging** the built-in tooling is
relatively thin:

| Pain point | What it looks like |
|---|---|
| Weak SQL editing assistance | The web editor lacks schema/table autocomplete, column completion, and syntax hints — especially painful across databases and data sources |
| Weak query history | Executed statements are hard to search and reuse; finding a query you wrote last week takes a while |
| Weak result-set handling | No way to turn query results into `INSERT` statements; migrating data or seeding test data means assembling SQL by hand |
| Fragmented data sources | Switching between data sources in the UI over and over; comparing across databases in a single session is impractical |

Those are exactly the strengths of desktop clients like DataGrip / Navicat: schema-aware
completion, local execution history, result-set editing and "generate INSERT / export",
multiple sessions and tabs.

sql-relay's approach is **not to replace Yearning, but to fill the gap in front of it**: put a
read-only proxy between the client and Yearning, so the client keeps its editing power while
permissions, approvals, and real credentials stay with Yearning.

- Security model unchanged: the proxy holds no database password, is read-only, rejects all
  writes, and data changes still go through Yearning tickets.
- Back to a native client experience: schema hints, history, and "results → INSERT" all work.
- Multiple data sources collapse into one connection; cross-database queries need no switching.

**Who it's for**: developers who already run Yearning and want to query data from a desktop
client such as DataGrip or Navicat. If submitting and approving tickets in the browser is all
you need, Yearning alone is enough and you don't need this tool.

## How It Works

```
DataGrip ──MySQL protocol──► local proxy (sql-relay) ──HTTP/WebSocket──► Yearning backend
                             │ routes by schema + blocks writes        │ queries real MySQL
                             └─ config.yaml                              with real credentials
```

- **Auth**: `POST /ldap` (or `/login`) to obtain a JWT, cached for reuse.
- **Data sources**: `GET /api/v2/fetch/source?tp=query` lists queryable sources.
- **Query**: WebSocket `ws://<base>/api/v2/query/results?source_id=...`, with the JWT in
  `Sec-WebSocket-Protocol` and a same-origin `Origin` header; sends msgpack
  `{"type":4,"sql":...,"schema":...}` and receives a `queryResults` set.
- **Read-only enforcement**: writes / DDL / `SELECT ... INTO` / `CALL` / locking reads are all
  rejected with a MySQL error.
- **Cross-source aggregation**: routes by schema within a single connection; `SHOW DATABASES`
  returns every database of every data source.

## Features

### 1. All data sources in one connection

The proxy walks every data source your account can query, fetches each one's database list,
and builds a global "schema → data source" map. Therefore:

- `SHOW DATABASES` / `SELECT ... FROM information_schema.SCHEMATA` are answered locally with
  **all databases of all sources** (deduplicated and sorted).
- `SELECT * FROM db.table` is routed automatically to the owning data source.
- After `USE db`, unqualified queries switch to that database's data source.
- WebSocket sessions are cached per `source_id`, so switching schemas doesn't reconnect.

> Note: if the same database name exists under multiple sources, the first match wins.

### 2. Automatic query ticket handling

When Yearning has query review enabled, a WebSocket query requires an approved ticket
(`status=2`). On a "ticket not approved" response, the proxy calls `POST /api/v2/query/post`
to file one and retries:

- Review disabled: the ticket is created with `status=2` and the retry succeeds transparently;
- Review enabled: a `status=1` ticket is created and you are told to wait for approval.

### 3. Session statements handled locally

`SET` (`SET NAMES` / `SET sql_mode` / `SET autocommit`, …) and `USE` are handled by the proxy
and never forwarded to Yearning's query engine (it cannot parse `SET` and would return
`Query was empty`).

### 4. Schema metadata and local caching

Clients (especially DataGrip's database tree and table view) query metadata heavily:

- `SHOW TABLES` / `SHOW COLUMNS` and `information_schema.TABLES` / `COLUMNS` / `STATISTICS`
  hit a **process-level local cache** shared across connections, with a TTL from
  `timeout.metadata_cache` (default 5 minutes).
- `SELECT @@version` / `SHOW VARIABLES` / `SHOW STATUS`, plus `information_schema` privilege,
  charset, collation, engine and plugin tables, are answered locally with empty results so the
  client doesn't error out.
- DDL probes against system objects (`information_schema.*`, `mysql.*`,
  `performance_schema.*`, `sys.*`), e.g. `SHOW CREATE VIEW`, are suppressed locally.

### 5. Client compatibility rewriting

- **Prepared statements**: `PREPARE` / `EXECUTE` have their parameters inlined into plain SQL
  before forwarding (Yearning's query channel doesn't speak the prepared-statement protocol).
- **MySQL 5.6 backend compatibility**: queries against `information_schema.COLUMNS` that
  include the `generation_expression` column (5.7+ only) have that column stripped before
  being sent down, avoiding "unknown column" errors.

## Configuration

Copy `config.yaml` and edit it:

```yaml
listen: 127.0.0.1:3307          # local MySQL protocol listen address (loopback only)
server_version: "5.7.36"        # advertised server version, drives mysql_native_password
proxy_password: "change-me"     # the password field in DataGrip
yearning:
  base_url: "http://yearning.example.com"   # http/https; ws/wss derived automatically
  auth_mode: "ldap"             # ldap -> POST /ldap; general -> POST /login
  login_user: "your-account"
  login_password: "your-password"
  is_ldap: true                 # matches Yearning /ldap parameters
  is_oidc: false
timeout:
  query: 120s                   # per-query timeout (default 60s)
  connect: 20s                  # HTTP/WebSocket connect timeout (default 10s)
  metadata_cache: 5m            # local schema metadata cache TTL (default 5m)
```

All three `timeout` entries are optional; the defaults in parentheses apply when omitted.
`server_version` defaults to `5.7.36`.

## Build and Run

```bash
# requires Go >= 1.21
go mod tidy
go build -o sql-relay .

./sql-relay -config config.yaml
```

Cross-compiling (produce Windows/Linux/macOS binaries from any platform; output names match
those referenced in the [User Guide](USAGE_EN.md)):

```bash
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o sql-relay-windows-amd64.exe .
GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build -o sql-relay-linux-amd64 .
GOOS=darwin  GOARCH=amd64 CGO_ENABLED=0 go build -o sql-relay-darwin-amd64 .
```

The binary is statically compiled: distributing it requires only the executable plus
`config.yaml`, and the target machine needs no Go toolchain.

## Connecting from DataGrip

| Field | Value |
|---|---|
| Host | `127.0.0.1` |
| Port | `3307` (same as `listen` in the config) |
| User | a Yearning data source name (e.g. `xx_order`) — the default source for unqualified queries |
| Password | `proxy_password` |
| Driver | any MySQL driver (the proxy advertises 5.7) |

It is recommended to disable "Execute statements atomically" in the data source settings.

Once connected, `SHOW DATABASES` lists every database of every source, and you can query
across databases directly with `SELECT * FROM db.table`.

## Notes and Limitations

- Depends on Yearning's query review state: the account needs an approved query ticket
  (`status=2`), or Yearning must have query review disabled; the proxy files a ticket
  automatically (see Features).
- `SET` is currently absorbed and allowed locally (harmless for a read-only connection); a
  strict allowlist could be added if needed.
- Result values are all returned as strings, so numeric sorting/filtering happens as string
  comparison on the client.
- Loopback-only listening plus `proxy_password` provide two layers of protection against other
  local processes connecting.
- Comment stripping uses a quote-aware state machine, so `--`, `#` and `/* */` inside string
  literals are not misread — still, don't try to bypass the read-only guard on this connection.

## Project Layout

```
sql-relay/
├── main.go                       # entry: listen, handshake, command loop, close safety
├── config.yaml                   # configuration template
├── 使用说明.md                    # user guide (Chinese): setup/start/connect/FAQ
├── README_EN.md                  # this file — English README
├── USAGE_EN.md                   # English user guide
├── LICENSE                       # MIT
├── THIRD_PARTY_NOTICES.md        # third-party copyright and license notices
└── internal/
    ├── config/                   # config loading and validation
    ├── yearning/                 # Yearning HTTP + WebSocket client, tickets, schema mapping
    ├── msgpack/                  # QueryDeal / queryResults codec
    ├── classifier/               # comment-stripping state machine + read-only decision
    ├── resultset/                # queryResults -> mysql.Result conversion
    ├── metacache/                # process-level metadata cache (configurable TTL)
    └── proxy/                    # go-mysql server.Handler: routing, aggregation, enforcement
```

## Testing

```bash
go test ./...
```

Covers comment stripping / read-only enforcement, msgpack codec, result-set conversion, and the
Yearning client login / data-source listing (including a lock-reentrancy deadlock regression).

## License

This project is licensed under [MIT](LICENSE).

Copyright and license notices for dependencies live in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md), which includes the full Apache-2.0 text for
`pingcap/tidb/pkg/parser` and `pingcap/log`, plus the NOTICE file of `gopkg.in/yaml.v3`.
