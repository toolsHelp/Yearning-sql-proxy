# sql-relay User Guide

中文版: [使用说明.md](使用说明.md)

sql-relay is a **read-only SQL relay proxy**: it impersonates a MySQL 5.7 server so that
standard MySQL clients such as DataGrip and Navicat can connect directly. Read-only queries are
forwarded to the **Yearning backend** for execution and results are returned over the MySQL
protocol. The real database credentials are always held by Yearning — the proxy only keeps your
Yearning login password locally and never writes any database password to disk.

---

## 1. What You Need

1. The executable for your operating system (download it from the Releases page, or build it
   yourself by following [README.md](README.md));
2. A copy of the `config.yaml` template (copy it and fill in your own values — see the next
   section);
3. A MySQL client (DataGrip recommended; Navicat / DBeaver also work).

| Your system | Which file |
|---|---|
| Windows 64-bit | `sql-relay-windows-amd64.exe` |
| Linux 64-bit | `sql-relay-linux-amd64` |
| macOS 64-bit (Intel) | `sql-relay-darwin-amd64` |

> The binaries are statically compiled — **no Go toolchain or other runtime dependency is
> required**.

---

## 2. Configure `config.yaml`

Open `config.yaml` and replace these entries with your real values (you can leave the rest
alone):

```yaml
proxy_password: "your-password"               # your own local connection password

yearning:
  base_url: "http://your-yearning-host"       # no trailing slash
  login_user: "your-yearning-account"
  login_password: "your-yearning-password"
```

The remaining options:

| Option | Meaning |
|---|---|
| `listen` | Local listen address, default `127.0.0.1:3307`, **loopback only** — no need to change it |
| `timeout.query` | Per-query timeout, default 120s; raise it for slow queries |
| `timeout.connect` | Connection timeout, default 20s |
| `timeout.metadata_cache` | How long schema metadata is cached locally, default 5m |

---

## 3. Start the Proxy

### Windows (PowerShell / CMD)

```powershell
cd into the folder
.\sql-relay-windows-amd64.exe -config config.yaml
```

### Linux / macOS

```bash
cd into the folder
chmod +x sql-relay-linux-amd64        # first time only
./sql-relay-linux-amd64 -config config.yaml
```

On success the terminal prints something like:

```
Yearning 数据源: xxx (source_id=...)
sql-relay 只读代理监听 127.0.0.1:3307，伪装版本 5.7.36，可用数据源: [...]
```

**Keep this window open** — the proxy is running as long as it is. Press `Ctrl+C` to quit.

> **On Windows, if you see "Windows protected your PC" or "Unknown publisher" on first run:**
> this happens because the binary is not commercially code-signed (code-signing certificates
> are billed annually and this project does not buy one). It is expected, not malware. To
> proceed: click **More info** → **Run anyway**. If the file was downloaded and is still
> blocked, right-click it → **Properties** → check **Unblock** → OK, then run it again.

---

## 4. Connect from DataGrip

| Field | Value |
|---|---|
| Host | `127.0.0.1` |
| Port | `3307` (same as `listen` in your config) |
| User | **a Yearning data source name** (e.g. `gaia_purchase_order` — see the "可用数据源" list printed at startup) |
| Password | the `proxy_password` from `config.yaml` |
| Driver | any MySQL driver (the proxy advertises 5.7) |

It is recommended to disable "Execute statements atomically" in the data source settings.

Once connected:

- `SHOW DATABASES` lists **every database of every data source** your account can access;
- `SELECT * FROM db.table` works across databases — the proxy routes by schema automatically;
- after `USE db`, unqualified table names also work.

---

## 5. Important Limitations

1. **Read-only**: writes / DDL (`ALTER` / `CREATE` / `DROP`, …) / `SELECT ... INTO` / `CALL` /
   locking reads are all rejected with a MySQL error. To change data, file a ticket in Yearning.
2. **Values come back as strings**: sorting and filtering on numeric columns happens as string
   comparison on the client (use `CAST` or convert client-side if you need exact numerics).
3. Loopback-only listening plus `proxy_password` provide two layers of protection against other
   local processes connecting.

---

## 6. FAQ

**Q: "Access denied for user ... to database ..." or "No database selected"?**
A: Make sure User is a name from the "可用数据源" list printed at startup, and always qualify
cross-database queries with the schema prefix (e.g. `db.table`).

**Q: DataGrip shows an incomplete table structure / reports a low compatibility level?**
A: The proxy is read-only: `SHOW CREATE` can only return DDL for business tables and real
`ALTER`s are blocked. This is expected.

**Q: It says my query ticket is not approved?**
A: The proxy tries to file a query ticket automatically once; if Yearning has review enabled,
the ticket needs to be approved in Yearning before the query can run.
