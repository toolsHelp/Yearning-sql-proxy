# sql-relay — 只读 SQL 中转代理

[![CI](https://github.com/toolsHelp/Yearning-sql-proxy/actions/workflows/ci.yml/badge.svg)](https://github.com/toolsHelp/Yearning-sql-proxy/actions/workflows/ci.yml)

程序名 `sql-relay`（仓库名 Yearning-sql-proxy，二者指同一个项目）。
English: [README_EN.md](README_EN.md)

> **免责声明**
> 本项目是面向 [Yearning](https://github.com/cookieY/Yearning) 的第三方只读查询代理工具，
> 与 Yearning 官方无关，非其官方项目、分支或发行版，也未获其维护者认可。
> MySQL 是 Oracle Corporation 及其关联公司的注册商标；本项目仅实现 MySQL 网络协议以兼容标准
> MySQL 客户端，不属于 Oracle，亦未获其认可或授权。

伪装成 MySQL 5.7 服务端，让 DataGrip / Navicat 等标准 MySQL 客户端直连，
把只读查询请求转发到 **Yearning 后端** 执行，并把结果按 MySQL 协议返回。

后端 MySQL 的真实凭据始终由 Yearning 服务端持有，代理本地只保存 Yearning 的登录口令，
不落盘任何数据库密码。

> **只想用起来？** 如果你拿到的是编译好的可执行文件、只想连上 DataGrip 查数据，
> 请直接看 **[使用说明.md](使用说明.md)**（英文版 [USAGE_EN.md](USAGE_EN.md)）：
> 改配置 → 启动 → 建连接 → 常见问题。本文件剩余部分主要面向构建与二次开发。

## 背景（为什么要做这个）

Yearning 在企业里通常承担**数据库审批平台**的角色：线上 DDL / DML 一律走工单审批，
数据库真实凭据由平台统一持有，开发人员不直接接触。这套模式在安全合规上很有价值，
但在**日常查询与开发调试**环节，它的能力相对薄弱：

| 痛点 | 具体表现 |
|---|---|
| SQL 编写提示弱 | Web 端编辑器缺少库表结构联想、字段补全与语法提示，跨库、多数据源时尤其吃力 |
| SQL 历史记录弱 | 执行过的语句难以检索和复用，想找回上周写过的那条查询往往要翻很久 |
| 结果集操作弱 | 不支持把查询结果直接转写成 `INSERT` 语句，迁移数据或造测试数据时只能手工拼 |
| 多数据源割裂 | 查另一个数据源要在页面上反复切换，很难在一个会话里跨库比对 |

而这些恰好是 DataGrip / Navicat 这类本地客户端的强项：结构联想与补全、本地执行历史、
结果集编辑与「转写 INSERT / 导出」、多会话与多标签管理。

sql-relay 的思路是**不替代 Yearning，而是补上它前面这一环**：在客户端与 Yearning 之间
加一层只读代理，让客户端照常用它的编辑能力，而权限、审批、真实库凭据仍然由 Yearning 把关。

- 安全模型不变：代理不持有任何数据库密码，只读，写操作一律拦截，改数据仍走 Yearning 工单；
- 使用体验回到本地客户端：结构提示、历史记录、结果集转写 INSERT 全部可用；
- 多个数据源合并成一个连接，跨库查询不必来回切换。

**适合谁用**：已经部署了 Yearning、又想用 DataGrip / Navicat 这类本地客户端查数的开发。
如果你只需要在网页上提交和审批工单，Yearning 本身就够用，不必引入本工具。

## 工作原理

```
DataGrip ──MySQL协议──► 本地代理(sql-relay) ──HTTP/WebSocket──► Yearning 后端
                         │  按库名路由 + 只读拦截                   │  真凭据查真实 MySQL
                         └─ config.yaml
```

- **认证**：`POST /ldap`（或 `/login`）换取 JWT，缓存复用。
- **数据源**：`GET /api/v2/fetch/source?tp=query` 拉取可查询数据源列表。
- **查询**：WebSocket `ws://<base>/api/v2/query/results?source_id=...`，
  `Sec-WebSocket-Protocol` 携带 JWT 且带同源 `Origin` 头，
  msgpack 发送 `{"type":4,"sql":...,"schema":...}`，接收 `queryResults` 结果集。
- **只读拦截**：写语句 / DDL / `SELECT ... INTO` / `CALL` / 加锁读 等一律拒绝，返回 MySQL 错误。
- **跨数据源汇总**：一个连接内按库名自动路由到对应数据源，`SHOW DATABASES` 返回所有数据源的全部库。

## 功能特性

### 1. 一个连接汇总所有数据源

代理会遍历账号有查询权限的**所有数据源**，为每个数据源拉取其库列表，建立
「库名 → 数据源」的全局映射。于是：

- `SHOW DATABASES` / `SELECT ... FROM information_schema.SCHEMATA` 本地返回**全部数据源的所有库**（去重、排序）。
- `SELECT * FROM 某库.某表` 时自动按库名路由到所属数据源执行。
- `USE 某库` 后，无前缀的查询自动切到该库所属的数据源。
- 按 `source_id` 缓存多个 WebSocket 会话，切换库时不重复建连。

> 注意：多个数据源下若有**同名库**，映射保留第一个匹配的数据源；如遇查错库可反馈调整优先级。

### 2. 查询工单自动处理

Yearning 开启查询审核时，WebSocket 查询需要账号存在已批准的查询工单（`status=2`）。
代理在遇到「工单未批准」时会自动调 `POST /api/v2/query/post` 提交一次查询工单并重试：

- 审核关闭：工单自动生成 `status=2`，重试直接成功（对用户无感）；
- 审核开启：生成 `status=1` 待审工单，提示等待审批。

### 3. 会话本地语句本地消化

`SET`（`SET NAMES` / `SET sql_mode` / `SET autocommit` 等）与 `USE` 在代理侧处理，
不转发给 Yearning 的查询引擎（其解析不了 `SET`，会报 `Query was empty`）。

### 4. 表结构元数据与本地缓存

客户端（尤其 DataGrip 的库表树、表结构视图）会大量查询元数据，代理对这部分做了本地处理：

- `SHOW TABLES` / `SHOW COLUMNS` 与 `information_schema.TABLES` / `COLUMNS` / `STATISTICS`
  命中**进程级本地缓存**，TTL 由 `timeout.metadata_cache` 控制（默认 5 分钟），跨连接共享。
- `SELECT @@version` / `SHOW VARIABLES` / `SHOW STATUS` 及 `information_schema` 的权限、
  字符集、引擎、插件等表，Yearning 查询引擎处理不了，代理本地返回空集，避免客户端报错。
- 系统对象（`information_schema.*` / `mysql.*` / `performance_schema.*` / `sys.*`）的
  DDL 探测（如 `SHOW CREATE VIEW`）本地屏蔽。

### 5. 客户端兼容性改写

- **预处理语句**：`PREPARE / EXECUTE` 在代理侧把参数内联成普通 SQL 再转发
  （Yearning 的查询通道不接受预处理协议）。
- **后端 MySQL 5.6 兼容**：`information_schema.COLUMNS` 查询若包含 `generation_expression`
  列（5.7+ 才有），会自动剥离该列后再下发，避免后端报未知列。

## 配置

复制 `config.yaml` 并修改：

```yaml
listen: 127.0.0.1:3307          # 本地 MySQL 协议监听地址（仅回环）
server_version: "5.7.36"        # 伪装的服务端版本，促使客户端用 mysql_native_password
proxy_password: "change-me"     # DataGrip 的 password 字段
yearning:
  base_url: "http://yearning.example.com"   # 支持 http/https，ws/wss 自动推导
  auth_mode: "ldap"             # ldap -> POST /ldap；general -> POST /login
  login_user: "your-account"
  login_password: "your-password"
  is_ldap: true                 # 与 Yearning /ldap 入参一致
  is_oidc: false
timeout:
  query: 120s                    # 单次查询超时（默认 60s）
  connect: 20s                   # HTTP/WebSocket 建连超时（默认 10s）
  metadata_cache: 5m             # 表结构元数据本地缓存 TTL（默认 5m）
```

`timeout` 三段均可省略，省略时使用上表括号内的默认值；`server_version` 省略时为 `5.7.36`。

## 构建与运行

```bash
# 依赖 Go >= 1.21
go mod tidy
go build -o sql-relay .

./sql-relay -config config.yaml
```

跨平台编译（在任意平台生成 Windows/Linux/macOS 可执行文件，
产物名与 [使用说明.md](使用说明.md) 中提到的文件名保持一致）：

```bash
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o sql-relay-windows-amd64.exe .
GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build -o sql-relay-linux-amd64 .
GOOS=darwin  GOARCH=amd64 CGO_ENABLED=0 go build -o sql-relay-darwin-amd64 .
```

程序为静态编译，分发时只需「可执行文件 + `config.yaml`」，目标机器无需安装 Go。

## DataGrip 连接约定

| 项 | 值 |
|---|---|
| Host | `127.0.0.1` |
| Port | `3307`（与 config 一致） |
| User | Yearning 数据源名（如 `gaia_purchase_order`），作为无库名前缀查询时的默认数据源 |
| Password | `proxy_password` |
| Driver | 任意 MySQL 驱动（代理伪装为 5.7） |

建议在数据源设置里关掉 "Execute statements atomically"。

连接后 `SHOW DATABASES` 会显示所有数据源的全部库，直接 `SELECT * FROM 库名.表名` 即可跨库查询。

## 说明与限制

- 依赖 Yearning 的查询审核状态：当前账号需有已批准的查询工单（`status=2`），
  或 Yearning 已关闭查询审核；代理会自动提交工单（见「功能特性」）。
- `SET` 目前在代理本地消化并放行（只读连接基本无害），如需更严格可白名单化。
- 结果集类型当前全部按字符串返回，数值排序/筛选在客户端按字符串比较。
- 仅监听回环 + `proxy_password` 双重防护，防止本机其他进程蹭连。
- 语句注释剥离采用带引号状态机的实现，字符串字面量内的 `--`/`#`/`/* */` 不会被误判，
  但仍建议不要在只读连接上尝试绕过拦截。

## 目录结构

```
sql-relay/
├── main.go                       # 入口：监听、握手、命令循环、连接关闭防护
├── config.yaml                   # 配置模板
├── 使用说明.md                    # 面向使用者的上手文档（配置/启动/连接/FAQ）
├── README_EN.md                  # 英文版说明文档
├── USAGE_EN.md                   # 英文版使用说明
├── LICENSE                       # MIT
├── THIRD_PARTY_NOTICES.md        # 依赖组件的版权与许可证声明
└── internal/
    ├── config/                   # 配置加载与校验
    ├── yearning/                 # Yearning HTTP + WebSocket 客户端、查询工单、schema 映射
    ├── msgpack/                  # QueryDeal / queryResults 编解码
    ├── classifier/               # 注释剥离状态机 + 只读拦截判定
    ├── resultset/                # queryResults -> mysql.Result 转换
    ├── metacache/                # 进程级元数据缓存（TTL 可配）
    └── proxy/                    # go-mysql server.Handler：多会话路由、库聚合、只读拦截
```

## 测试

```bash
go test ./...
```

覆盖：注释剥离/只读拦截、msgpack 编解码、结果集转换、以及 Yearning 客户端登录/数据源拉取（含锁重入死锁回归测试）。

## 许可证

本项目采用 [MIT](LICENSE) 许可证。

依赖组件的版权与许可证声明见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)，
其中含 Apache-2.0 组件（pingcap/tidb/pkg/parser、pingcap/log）与 `gopkg.in/yaml.v3` 的 NOTICE 原文。
