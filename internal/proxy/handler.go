// Package proxy 实现 go-mysql server.Handler，把只读查询转发到 Yearning 后端。
package proxy

import (
	"errors"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/server"

	"github.com/toolsHelp/Yearning-sql-proxy/internal/audit"
	"github.com/toolsHelp/Yearning-sql-proxy/internal/classifier"
	"github.com/toolsHelp/Yearning-sql-proxy/internal/config"
	"github.com/toolsHelp/Yearning-sql-proxy/internal/metacache"
	"github.com/toolsHelp/Yearning-sql-proxy/internal/msgpack"
	"github.com/toolsHelp/Yearning-sql-proxy/internal/resultset"
	"github.com/toolsHelp/Yearning-sql-proxy/internal/yearning"
)

// regexShowDB 匹配 SHOW DATABASES。
var regexShowDB = regexp.MustCompile(`(?i)^show\s+databases\s*;?$`)

// reSchemaFilter 匹配 information_schema 查询里的库名过滤条件。
//
// 覆盖各类 *_schema 列（table_schema / routine_schema / trigger_schema /
// event_schema / specific_schema / schema_name 等），两个捕获组分别对应
// `= '库'` 与 `in ('库', ...)` 两种写法。
//
// 关键约束：值必须带引号（或以数字开头），否则会把 `T.table_schema = V.table_schema`
// 这类 JOIN 条件里的列标识符误当成库名。DataGrip 的「Retrieve Tables and Views」
// 正是这种写法，一旦误判就会去查一个不存在的库、返回 0 行。
var reSchemaFilter = regexp.MustCompile(
	`(?i)[a-z_]*schema(?:_name)?\s*\)?\s*(?:=\s*['"\x60]([A-Za-z0-9_$]+)['"\x60]|in\s*\(\s*['"\x60]([A-Za-z0-9_$]+)['"\x60])`)

// Handler 是每个客户端连接独享的 handler 实例，
// 记录该连接的 DataGrip user（Yearning 数据源名）与后端会话。
type Handler struct {
	cfg  *config.Config
	yc   *yearning.Client
	user string // 该连接的 DataGrip user，即 Yearning 数据源名（默认数据源）

	// 多 source 会话缓存：一个连接内按库名自动切换到对应 source_id 的 WS 会话。
	mu       sync.Mutex
	sessions map[string]*yearning.QuerySession // source_id -> 会话
	schema   string                            // 当前 USE 的库名
	connID   uint32                            // go-mysql 连接 ID，用于审计关联

	// 表结构元数据缓存（进程级，跨连接共享）
	meta *metacache.Cache
	// 请求审计日志，nil 表示未启用。
	aud *audit.Logger
}

// NewHandler 构造一个连接到 yearn 的连接 handler。
// metaCache 为进程级元数据缓存（跨连接共享），nil 时内部新建一个。
func NewHandler(cfg *config.Config, yc *yearning.Client, metaCache *metacache.Cache) *Handler {
	if metaCache == nil {
		metaCache = metacache.New(cfg.Timeout.MetadataCache)
	}
	return &Handler{cfg: cfg, yc: yc, sessions: make(map[string]*yearning.QuerySession), meta: metaCache}
}

// SetUser 由连接建立后写入该连接的 DataGrip user（Yearning 数据源名）。
func (h *Handler) SetUser(user string) {
	h.user = user
}

// SetConnID 写入 go-mysql 的连接 ID，供审计事件关联同一连接。
func (h *Handler) SetConnID(id uint32) {
	h.mu.Lock()
	h.connID = id
	h.mu.Unlock()
}

// SetAudit 绑定审计日志（nil 表示不记录）。
func (h *Handler) SetAudit(l *audit.Logger) {
	h.mu.Lock()
	h.aud = l
	h.mu.Unlock()
}

// auditLogger 取当前审计日志（可为 nil）。
func (h *Handler) auditLogger() *audit.Logger {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.aud
}

// rec 记录一条审计事件；未启用审计时为空操作。
func (h *Handler) rec(e *audit.Event) {
	if l := h.auditLogger(); l != nil {
		l.Record(*e)
	}
}

// sessionFor 返回（必要时新建）指定 source_id 的查询会话。
//
// 建连（握手 + 可能的重新登录）可能耗时数百毫秒，聚合模式下还要为多个数据源
// 建连，因此不能持 h.mu 做这件事，否则会阻塞同一连接上的其它查询。
func (h *Handler) sessionFor(sourceID string) (*yearning.QuerySession, error) {
	h.mu.Lock()
	if qs, ok := h.sessions[sourceID]; ok && !qs.IsDead() {
		h.mu.Unlock()
		return qs, nil
	}
	// 已死或不存在：清理后重建。先释放锁再建连。
	h.dropSessionLocked(sourceID)
	h.mu.Unlock()

	qs, err := h.yc.NewQuerySession(sourceID)
	if err != nil {
		// 可能是 token 过期，清缓存重试一次。
		h.yc.ClearToken()
		qs, err = h.yc.NewQuerySession(sourceID)
		if err != nil {
			return nil, err
		}
	}

	// 并发建连时以先放入的为准，多余的连接关掉，避免同一 source 多个会话并存。
	h.mu.Lock()
	defer h.mu.Unlock()
	if existing, ok := h.sessions[sourceID]; ok && !existing.IsDead() {
		qs.Close()
		return existing, nil
	}
	h.sessions[sourceID] = qs
	return qs, nil
}

// dropSession 移除并关闭指定 source_id 的会话（用于断连后重建）。
func (h *Handler) dropSession(sourceID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.dropSessionLocked(sourceID)
}

func (h *Handler) dropSessionLocked(sourceID string) {
	if qs, ok := h.sessions[sourceID]; ok {
		qs.Close()
		delete(h.sessions, sourceID)
	}
}

// resolveSource 根据当前库名/schema 解析目标 source_id。
// 优先用全局 schema→source 映射（尚未加载时先触发加载）；退化为 user 指向的数据源。
func (h *Handler) resolveSource(schema string) (string, error) {
	if schema != "" {
		if id, ok := h.yc.SourceBySchema(schema); ok {
			return id, nil
		}
		// 映射尚未建立（SchemaMap 懒加载，只有 SHOW DATABASES 才触发）时，
		// 主动加载一次，避免「知道库名却退回 user 的错误 source」。
		if _, err := h.yc.SchemaMap(); err != nil {
			log.Printf("加载 schema→source 映射失败: %v", err)
		} else if id, ok := h.yc.SourceBySchema(schema); ok {
			return id, nil
		}
	}
	// 退回：按 user 匹配数据源名（兼容为每个数据源单独建连接的用法）。
	if h.user != "" {
		if id, err := h.yc.ResolveSourceID(h.user); err == nil {
			return id, nil
		}
	}

	// 聚合模式：user 留空、填库名或填了不认识的名字时，不报错，
	// 从全局「库名→数据源」映射里取一个可用数据源兜底。
	// 这样用户在 DataGrip 里只配一个连接，就能看到账号下有权限的所有库。
	sm, err := h.yc.SchemaMap()
	if err != nil {
		return "", fmt.Errorf("加载数据源与库列表失败: %w", err)
	}
	if len(sm) == 0 {
		return "", fmt.Errorf("当前账号没有可查询的库，请检查 Yearning 权限配置")
	}
	// user 恰好是库名时优先用该库所属数据源。
	if id, ok := pickSchemaSource(sm, h.user); ok {
		log.Printf("user %q 未匹配数据源名，按库名解析到 source_id=%s", h.user, id)
		return id, nil
	}
	// 否则任选一个（按库名排序取第一个，保证结果稳定）。
	names := make([]string, 0, len(sm))
	for name := range sm {
		names = append(names, name)
	}
	sort.Strings(names)
	id := sm[names[0]]
	log.Printf("user %q 未匹配数据源名，聚合模式选用 source_id=%s（库 %s）", h.user, id, names[0])
	return id, nil
}

// pickSchemaSource 在库名→数据源映射里按库名取数据源（大小写不敏感）。
func pickSchemaSource(sm map[string]string, name string) (string, bool) {
	if name == "" {
		return "", false
	}
	if id, ok := sm[name]; ok {
		return id, true
	}
	lower := strings.ToLower(name)
	for schema, id := range sm {
		if strings.ToLower(schema) == lower {
			return id, true
		}
	}
	return "", false
}

// myError 把普通字符串转成 MySQL 错误（透传给 DataGrip）。
func myError(code uint16, msg string) error {
	return mysql.NewError(code, msg)
}

// queryError 把底层错误转成给客户端的 MySQL 错误。
// 连接类错误给出可重试的提示，避免把 Go 的 websocket 错误原文直接抛给 DataGrip。
func queryError(err error) error {
	if err == nil {
		return nil
	}
	var me *mysql.MyError
	if errors.As(err, &me) {
		return me
	}
	if yearning.IsConnError(err) {
		return myError(1105, "与 Yearning 的连接已断开，请重试该查询")
	}
	return myError(1105, err.Error())
}

// connIDLocked 读取本连接的 go-mysql 连接 ID。
func (h *Handler) connIDLocked() uint32 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.connID
}

// rowsOf 取结果集行数，便于审计判断返回是否为空。
func rowsOf(res *mysql.Result) int {
	if res == nil || res.Resultset == nil {
		return 0
	}
	return len(res.Values)
}

// errInfo 拆出 MySQL 错误码与错误信息，供审计分类统计。
func errInfo(err error) (uint16, string) {
	if err == nil {
		return 0, ""
	}
	return errCodeOf(err), err.Error()
}

// errCodeOf 提取 MySQL 错误码（非 MySQL 错误返回 0）。
func errCodeOf(err error) uint16 {
	if err == nil {
		return 0
	}
	var me *mysql.MyError
	if errors.As(err, &me) {
		return me.Code
	}
	return 0
}

// UseDB 处理 COM_INIT_DB：记录当前库并放行。
func (h *Handler) UseDB(dbName string) error {
	h.mu.Lock()
	h.schema = dbName
	h.mu.Unlock()
	h.rec(&audit.Event{
		ConnID:   h.connIDLocked(),
		User:     h.user,
		Cmd:      audit.CmdInitDB,
		Kind:     string(classifier.KindSessionUse),
		Decision: audit.DecisionLocalUse,
		Schema:   dbName,
	})
	return nil
}

// HandleQuery 处理 COM_QUERY：只读校验后转发 Yearning 执行。
// 本方法只负责审计打点，真正的处理链路在 execQuery 中。
func (h *Handler) HandleQuery(query string) (*mysql.Result, error) {
	start := time.Now()
	ev := &audit.Event{
		Time:   start,
		ConnID: h.connIDLocked(),
		User:   h.user,
		Cmd:    audit.CmdQuery,
		SQL:    query,
	}
	var (
		res *mysql.Result
		d   audit.Decision
		err error
	)
	defer func() {
		ev.Decision = d
		ev.Rows = rowsOf(res)
		ev.LatencyMS = time.Since(start).Milliseconds()
		ev.ErrCode, ev.Error = errInfo(err)
		h.rec(ev)
	}()
	res, d, err = h.execQuery(query, ev)
	return res, err
}

// execQuery 是 HandleQuery 的实现体，额外返回审计用的处理路径 Decision。
// ev 会被填充 Kind / Schema / Source 三个字段。
func (h *Handler) execQuery(query string, ev *audit.Event) (*mysql.Result, audit.Decision, error) {
	log.Printf("查询[%s]: %s", h.user, query)
	ev.Kind = string(classifier.Classify(query, ""))
	if err := classifier.CheckReadOnly(query); err != nil {
		log.Printf("查询被拦截: %v", err)
		return nil, audit.DecisionRejected, err
	}

	// 多条语句拆开逐条执行；DataGrip 默认单语句，这里取第一条的结果。
	stmts := classifier.SplitStatements(query)
	if len(stmts) == 0 {
		return nil, audit.DecisionError, myError(1064, "空语句")
	}
	q := stmts[0]
	fw := classifier.FirstWord(q)
	ev.Kind = string(classifier.Classify(q, fw))
	// 压缩传输体积：Yearning 的查询 WS 对单条消息有大小限制（55KB 实测会被断连），
	// DataGrip 格式化的长 SQL 压缩后通常能降 30%~60%。语义等价，字面量不动。
	if shrunk := classifier.ShrinkSQL(q); len(shrunk) < len(q) {
		q = shrunk
	}

	// USE 语句直接改本地记录的 schema，不转发给 Yearning 的 SQL 通道。
	if strings.EqualFold(fw, "use") {
		db := strings.TrimSpace(q[len("use"):])
		db = strings.Trim(db, "` \t")
		h.mu.Lock()
		h.schema = db
		h.mu.Unlock()
		ev.Schema = db
		return &mysql.Result{Status: 0x0002}, audit.DecisionLocalUse, nil
	}

	// 本地会话语句（SET / 事务控制）在代理侧消化，不转发给 Yearning。
	if isLocalSessionStmt(fw) {
		return &mysql.Result{Status: 0x0002}, audit.DecisionLocalSession, nil
	}

	// 本地汇总：SHOW DATABASES / information_schema.SCHEMATA 返回全部数据源的所有库。
	if r, handled := h.handleSchemaEnumeration(q, fw); handled {
		return r, audit.DecisionLocalServed, nil
	}

	// 本地屏蔽：DataGrip 的「用户/权限/排序规则」等管理类元数据探测，
	// Yearning 后端用受限账号，无这些系统表权限，转发会报错。返回空结果。
	if r, handled := handleManageInfoQuery(q); handled {
		return r, audit.DecisionLocalEmpty, nil
	}

	// 服务器特性探测：优先按伪装版本返回伪造值（SHOW VARIABLES / SELECT @@xx / VERSION() /
	// DATABASE()）。此前一律返回空结果，客户端拿不到版本号会按未知版本降级。
	if r, ok := h.fakeProbeResult(q, fw); ok {
		log.Printf("本地伪造服务器探测: %s", strings.TrimSpace(q))
		return r, audit.DecisionLocalServed, nil
	}

	// 本地屏蔽：系统变量/状态、information_schema 的 DDL 探测（SELECT @@xx / SHOW VARIABLES /
	// SHOW CREATE VIEW information_schema.xxx 等），Yearning 查询引擎处理不了，返回空。
	if handled := handleServerProbeQuery(q, fw); handled {
		return emptyResult(), audit.DecisionLocalEmpty, nil
	}

	// 改写：后端为 MySQL 5.6 时 information_schema.COLUMNS 没有 generation_expression 列，
	// DataGrip 9.x 驱动会查它，导致列查询失败。此处把该列剥成 NULL 别名。
	q = stripGenerationExpression(q)

	h.mu.Lock()
	schema := h.schema
	h.mu.Unlock()

	// 从 SQL 里提取目标库名（`db.table` 前缀 / WHERE TABLE_SCHEMA='x' / SHOW ... FROM x），
	// 命中则覆盖当前 schema，用于跨数据源路由。
	if db := extractTargetSchema(q); db != "" {
		schema = db
	}

	ev.Schema = schema

	// 该查询需要跨哪些数据源：SQL 里明确了库名就只查那个数据源，
	// 否则（聚合模式下的全量内省查询）需要遍历所有可用数据源合并结果。
	targets, err := h.targetSources(schema, q)
	if err != nil {
		return nil, audit.DecisionError, myError(1044, err.Error())
	}
	ev.Source = strings.Join(targets, ",")

	// 元数据缓存：可缓存的 information_schema 结构查询优先命中本地。
	// 单源查询按 source_id 作 key；跨数据源聚合查询按固定前缀作 key——
	// 没有这层缓存的话，DataGrip 每次内省都会对全部数据源打一遍聚合查询。
	cacheable := isCacheableMetaQuery(q)
	cacheKey := cacheKeyFor(targets)
	if cacheable {
		if r, ok := h.metaCacheGet(cacheKey, q); ok {
			log.Printf("查询[%s] 命中元数据缓存: %.80s", h.user, q)
			return r, audit.DecisionCacheHit, nil
		}
	}

	raw, ordered, err := h.execAcross(q, schema, targets)
	if err != nil {
		return nil, audit.DecisionError, queryError(err)
	}
	if logQueryDetail {
		log.Printf("查询[%s] 原始结果: %d 个结果集, %d 个字段, %d 行 (sql=%.120s)",
			h.user, len(raw.Results), rawFieldCount(raw), rawRowCount(raw), q)
	}
	res, err := resultset.Convert(raw)
	if err != nil {
		return nil, audit.DecisionError, myError(1105, err.Error())
	}

	// 成功结果写入缓存。
	if cacheable {
		h.metaCacheSet(cacheKey, q, raw)
	}
	if ordered {
		return res, audit.DecisionForwardedOrdered, nil
	}
	return res, audit.DecisionForwarded, nil
}

// handleManageInfoQuery 本地屏蔽 DataGrip 的管理类元数据探测（用户/权限/排序规则等），
// 返回结构正确的空结果集，避免转发给 Yearning 触发权限报错。
func handleManageInfoQuery(q string) (*mysql.Result, bool) {
	lower := strings.ToLower(q)
	if !isManageInfoQuery(lower) {
		return nil, false
	}
	cols := extractSelectColumns(q)
	if len(cols) == 0 {
		// 解析不出列名时给一个通用空结果，避免 DataGrip 报"未知列"。
		cols = []string{" "}
	}
	rs, err := mysql.BuildSimpleTextResultset(cols, nil)
	if err != nil {
		return nil, false
	}
	log.Printf("本地屏蔽管理类元数据查询: %s", strings.TrimSpace(q))
	return &mysql.Result{Status: 0x0002, Resultset: rs}, true
}

// isManageInfoQuery 判断是否为 DataGrip 的管理类信息探测。
func isManageInfoQuery(lower string) bool {
	// information_schema 里的权限/字符集/排序/引擎/插件等表
	infoTables := []string{
		"information_schema.user_privileges",
		"information_schema.schema_privileges",
		"information_schema.table_privileges",
		"information_schema.column_privileges",
		"information_schema.global_privileges",
		"information_schema.collations",
		"information_schema.collation_character_set_applicability",
		"information_schema.character_sets",
		"information_schema.engines",
		"information_schema.plugins",
		"information_schema.profiling",
	}
	for _, t := range infoTables {
		if strings.Contains(lower, t) {
			return true
		}
	}
	// mysql.* 系统库（mysql.user / mysql.db / mysql.procs_priv 等）
	if strings.Contains(lower, "from mysql.") || strings.Contains(lower, "join mysql.") {
		return true
	}
	// SHOW GRANTS / SHOW PRIVILEGES / SHOW PROFILE(S)
	if strings.Contains(lower, "show grants") ||
		strings.Contains(lower, "show privileges") ||
		strings.Contains(lower, "show profiles") {
		return true
	}
	return false
}

// extractSelectColumns 从 `SELECT a, b AS c FROM ...` 提取输出列名。
func extractSelectColumns(q string) []string {
	lower := strings.ToLower(q)
	sel := strings.Index(lower, "select")
	if sel < 0 {
		return nil
	}
	frm := strings.Index(lower[sel:], " from ")
	var segment string
	if frm >= 0 {
		segment = q[sel+len("select") : sel+frm]
	} else {
		segment = q[sel+len("select"):]
	}
	var cols []string
	for _, part := range strings.Split(segment, ",") {
		col := selectColumnName(part)
		if col == "" || col == "*" {
			continue
		}
		cols = append(cols, col)
	}
	return cols
}

// selectColumnName 从单个 SELECT 列表达式提取展示列名（优先 AS 别名）。
func selectColumnName(expr string) string {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return ""
	}
	// 去掉前导注释残留
	expr = strings.TrimSpace(expr)
	lower := strings.ToLower(expr)
	if idx := strings.Index(lower, " as "); idx >= 0 {
		name := strings.TrimSpace(expr[idx+len(" as "):])
		return strings.Trim(name, "`\"'")
	}
	// 无别名：取最后一段（去掉 表. 前缀）
	name := strings.TrimSpace(expr)
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	return strings.Trim(name, "`\"'")
}

// handleServerProbeQuery 识别 DataGrip 的服务器特性/DDL 探测查询，本地消化。
// 返回 true 表示已处理（应返回空结果）。
func handleServerProbeQuery(q, firstWord string) bool {
	lower := strings.ToLower(q)

	// SHOW VARIABLES / SHOW GLOBAL|SESSION (VARIABLES|STATUS)
	// 注意：不能用裸 " status" 匹配，否则 SHOW FUNCTION/PROCEDURE STATUS 会被误吞，
	// 导致 DataGrip 的函数/存储过程节点永远为空。
	if firstWord == "show" {
		if strings.Contains(lower, "show variables") ||
			strings.Contains(lower, "session variables") || strings.Contains(lower, "global variables") ||
			strings.Contains(lower, "show status") ||
			strings.Contains(lower, "session status") || strings.Contains(lower, "global status") {
			return true
		}
		// SHOW CREATE (VIEW|TABLE)：仅屏蔽系统对象（information_schema.* / mysql.*），
		// 真实业务表的 SHOW CREATE 需放行转发给 Yearning，否则 DataGrip 的
		// 「修改表」界面因拿不到 DDL 而提示「对象的内省级别低」。
		if strings.Contains(lower, "show create") {
			return isSystemDDLTarget(q)
		}
		// SHOW ENGINE / SHOW MASTER STATUS / SHOW SLAVE STATUS 等服务器状态
		if strings.Contains(lower, "show engine") || strings.Contains(lower, "show master") ||
			strings.Contains(lower, "show slave") || strings.Contains(lower, "show binary") ||
			strings.Contains(lower, "show binlog") || strings.Contains(lower, "show processlist") {
			return true
		}
	}

	// SELECT @@... 系统变量（单个或多个）/ version() 等函数
	if firstWord == "select" {
		if strings.Contains(q, "@@") {
			return true
		}
		// select version(), @@version_comment, database() —— 服务器特性探测
		if strings.Contains(lower, "version()") || strings.Contains(lower, "@@version") ||
			strings.Contains(lower, "database()") || strings.Contains(lower, "user()") ||
			strings.Contains(lower, "schema()") {
			// 纯函数/系统变量探测（不含业务表）→ 本地处理
			if !strings.Contains(lower, " from ") {
				return true
			}
		}
	}
	return false
}

// isSystemDDLTarget 判断一个 SHOW CREATE 的目标对象是否为系统对象
// （information_schema.* / mysql.* / performance_schema.* / sys.*）。
// 系统对象的 DDL 在受限后端账号下无法读取，需本地屏蔽；真实业务表放行转发。
func isSystemDDLTarget(q string) bool {
	lower := strings.ToLower(q)
	for _, sys := range []string{
		"information_schema.", "mysql.", "performance_schema.", "sys.",
	} {
		if strings.Contains(lower, sys) {
			return true
		}
	}
	return false
}

// stripGenerationExpression 把 information_schema.COLUMNS 查询里的 generation_expression
// 列替换为 NULL AS generation_expression，兼容后端 MySQL 5.6（无该列）。
func stripGenerationExpression(q string) string {
	lower := strings.ToLower(q)
	if !strings.Contains(lower, "generation_expression") {
		return q
	}
	return strings.ReplaceAll(q, "generation_expression", "cast(null as char(1)) as generation_expression")
}

// emptyResult 返回一个空结果集（单列占位）。
func emptyResult() *mysql.Result {
	rs, err := mysql.BuildSimpleTextResultset([]string{" "}, nil)
	if err != nil {
		return &mysql.Result{Status: 0x0002}
	}
	return &mysql.Result{Status: 0x0002, Resultset: rs}
}

// handleSchemaEnumeration 本地聚合所有数据源的库列表。
func (h *Handler) handleSchemaEnumeration(q, firstWord string) (*mysql.Result, bool) {
	isShowDB := firstWord == "show" && regexShowDB.MatchString(q)
	isSchemata := firstWord == "select" && strings.Contains(strings.ToLower(q), "information_schema.schemata")
	if !isShowDB && !isSchemata {
		return nil, false
	}
	sm, err := h.yc.SchemaMap()
	if err != nil {
		log.Printf("拉取全局库列表失败: %v", err)
		return nil, false // 交由后续逻辑处理
	}
	names := make([]string, 0, len(sm))
	for name := range sm {
		names = append(names, name)
	}
	sort.Strings(names)
	log.Printf("本地汇总 %d 个库", len(names))

	cols := []string{"Database"}
	if isSchemata {
		cols = []string{"SCHEMA_NAME"}
	}
	rs, err := mysql.BuildSimpleTextResultset(cols, stringsToRows(names))
	if err != nil {
		return nil, false
	}
	return &mysql.Result{Status: 0x0002, Resultset: rs}, true
}

// metaCacheKey 生成元数据缓存 key。
func (h *Handler) metaCacheKey(srcID, q string) string {
	return srcID + "|" + normalizeSQL(q)
}

func (h *Handler) metaCacheGet(srcID, q string) (*mysql.Result, bool) {
	v, ok := h.meta.Get(h.metaCacheKey(srcID, q))
	if !ok {
		return nil, false
	}
	raw, ok := v.(*msgpack.QueryResults)
	if !ok {
		return nil, false
	}
	r, err := resultset.Convert(raw)
	if err != nil {
		return nil, false
	}
	return r, true
}

func (h *Handler) metaCacheSet(srcID, q string, raw *msgpack.QueryResults) {
	if raw == nil || len(raw.Results) == 0 {
		return
	}
	h.meta.Set(h.metaCacheKey(srcID, q), raw)
}

// isCacheableMetaQuery 判断查询是否为可缓存的表结构元数据查询。
//
// 覆盖两条路径：
//  1. information_schema 结构表查询；
//  2. SHOW 语法的结构查询——真实抓包显示 DataGrip 2026.x 连接 5.7 服务端时
//     完全不发 information_schema，只用 SHOW FULL TABLES / SHOW FULL COLUMNS /
//     SHOW INDEX / SHOW CREATE TABLE，因此这部分必须一并缓存。
func isCacheableMetaQuery(q string) bool {
	lower := strings.ToLower(q)
	if strings.Contains(lower, "information_schema.") {
		// 只缓存结构类表：tables / columns / statistics / key_column_usage / views /
		// table_constraints / triggers / routines / parameters。
		for _, t := range []string{
			"information_schema.tables",
			"information_schema.columns",
			"information_schema.statistics",
			"information_schema.key_column_usage",
			"information_schema.views",
			"information_schema.table_constraints",
			"information_schema.triggers",
			"information_schema.routines",
			"information_schema.parameters",
		} {
			if strings.Contains(lower, t) {
				// 排除带 UNION 的聚合查询（多 schema 跨库），缓存粒度不一致。
				if strings.Contains(lower, "union") {
					return false
				}
				return true
			}
		}
		return false
	}
	return isCacheableShowMeta(lower)
}

// cacheableShowPrefixes 是可缓存的 SHOW 结构查询前缀。
var cacheableShowPrefixes = []string{
	"show tables", "show full tables", "show open tables",
	"show table status",
	"show columns", "show full columns", "show fields", "show full fields",
	"show index", "show indexes", "show keys",
	"show create table", "show create view",
}

// isCacheableShowMeta 判断是否可缓存的 SHOW 结构查询（入参已小写）。
func isCacheableShowMeta(lower string) bool {
	for _, p := range cacheableShowPrefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

// normalizeSQL 规范化 SQL 用于缓存 key：去掉注释、压缩空白、去末尾分号。
func normalizeSQL(q string) string {
	// 去掉 /* ... */ 注释
	q = stripBlockComments(q)
	// 去掉 -- 行注释
	lines := strings.Split(q, "\n")
	var out []string
	for _, ln := range lines {
		if i := strings.Index(ln, "--"); i >= 0 {
			ln = ln[:i]
		}
		out = append(out, strings.TrimSpace(ln))
	}
	q = strings.Join(out, " ")
	// 压缩连续空白
	q = strings.Join(strings.Fields(q), " ")
	q = strings.TrimSpace(strings.TrimSuffix(q, ";"))
	q = strings.TrimSpace(strings.TrimSuffix(q, " ;"))
	return q
}

// stripBlockComments 去掉 /* ... */ 块注释（简单实现，不处理字符串内）。
func stripBlockComments(q string) string {
	for {
		i := strings.Index(q, "/*")
		if i < 0 {
			break
		}
		j := strings.Index(q[i+2:], "*/")
		if j < 0 {
			q = q[:i]
			break
		}
		q = q[:i] + q[i+2+j+2:]
	}
	return q
}

// logQueryDetail 控制是否打印 Yearning 返回的原始结果规模。
// 排查「表/字段加载不出来」时置为 true，可看到每条查询实际返回的行数。
const logQueryDetail = false

// rawFieldCount / rawRowCount 汇总 Yearning 原始结果的字段数与行数。
func rawFieldCount(r *msgpack.QueryResults) int {
	n := 0
	for _, q := range r.Results {
		n += len(q.Field)
	}
	return n
}

func rawRowCount(r *msgpack.QueryResults) int {
	n := 0
	for _, q := range r.Results {
		n += len(q.Data)
	}
	return n
}

// targetSources 决定一条查询要打到哪些数据源。
//
//   - schema 有值：按「库名→数据源」映射定位；映射里没有该库时退化为 user 指定/兜底的数据源。
//   - schema 为空且是 information_schema 结构查询：说明客户端在问「全部库」，
//     需要遍历所有可用数据源后合并结果（聚合模式）。
//   - 其余情况：单个数据源。
func (h *Handler) targetSources(schema, q string) ([]string, error) {
	if schema != "" {
		if id, ok := h.yc.SourceBySchema(schema); ok {
			return []string{id}, nil
		}
		// 映射是懒加载的（只有 SHOW DATABASES / 聚合路径才触发），首次遇到具体库名时
		// 必须主动加载一次，否则会把该库的查询错误地兜底到别的数据源（查到 0 行）。
		if _, err := h.yc.SchemaMap(); err != nil {
			log.Printf("加载库→数据源映射失败: %v", err)
		} else if id, ok := h.yc.SourceBySchema(schema); ok {
			return []string{id}, nil
		}
		// 映射里确实没有这个库（新库或权限变化），退化为 user/兜底解析。
		log.Printf("库 %q 不在账号可访问的库列表中，退化为 user 指定的数据源", schema)
		id, err := h.resolveSource(schema)
		if err != nil {
			return nil, err
		}
		return []string{id}, nil
	}

	id, err := h.resolveSource("")
	if err != nil {
		return nil, err
	}
	if !needsAllSources(q) {
		return []string{id}, nil
	}
	all := h.yc.AllSourceIDs()
	if len(all) == 0 {
		return []string{id}, nil
	}
	return all, nil
}

// needsAllSources 判断这条「无库名限定」的查询是否需要跨数据源取全量。
// 仅限 information_schema 的结构元数据查询；业务查询仍走兜底数据源，
// 否则会拖慢普通查询并产生大量无意义的后端请求。
func needsAllSources(q string) bool {
	lower := strings.ToLower(q)
	if !strings.Contains(lower, "information_schema.") {
		return false
	}
	return true
}

// aggregateCachePrefix 是跨数据源聚合查询结果在元数据缓存里的 key 前缀。
const aggregateCachePrefix = "ALL"

// cacheKeyFor 生成元数据缓存的 source 维度 key：
// 单源用 source_id；多源聚合用固定前缀（同一 SQL 的聚合结果只算一次）。
func cacheKeyFor(targets []string) string {
	if len(targets) == 1 {
		return targets[0]
	}
	return aggregateCachePrefix
}

// largeSQLThreshold 超过该长度（压缩后）的 SQL，若重连重试仍触发断连，
// 判定为超出 Yearning 服务端消息大小限制，直接给出缩短提示而不再反复重试。
const largeSQLThreshold = 32 * 1024

// maxAggregateConcurrency 限制聚合查询时同时在途的「建连+查询」数量。
// 过大会在 Yearning 服务端触发连接限制/登录限流（表现为 websocket close sent 风暴），
// 过小则单条聚合查询耗时会明显上升。
const maxAggregateConcurrency = 6

// execAcross 在多个数据源上执行查询并合并结果（多数据源时做行级 UNION）。
func (h *Handler) execAcross(q, schema string, targets []string) (*msgpack.QueryResults, bool, error) {
	if len(targets) == 1 {
		id := targets[0]
		be, err := h.sessionFor(id)
		if err != nil {
			return nil, false, myError(1044, err.Error())
		}
		return h.execQueryRaw(be, id, q, schema)
	}

	// 并发查询各数据源：串行时 27 个数据源要累加各自的往返耗时（实测单条可达数秒）。
	// 用信号量限制同时在途的「建连+查询」，避免几十条 WebSocket 同时握手
	// 触发 Yearning 服务端的连接限制或登录限流（表现为 close sent 风暴）。
	// 每个数据源独立建连/查询，结果按 targets 顺序回填以保持输出稳定。
	type outcome struct {
		raw *msgpack.QueryResults
		ord bool
		err error
	}
	sem := make(chan struct{}, maxAggregateConcurrency)
	outcomes := make([]outcome, len(targets))
	var wg sync.WaitGroup
	for i, id := range targets {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			be, err := h.sessionFor(id)
			if err != nil {
				outcomes[i].err = err
				return
			}
			raw, ord, err := h.execQueryRaw(be, id, q, schema)
			outcomes[i] = outcome{raw: raw, ord: ord, err: err}
		}(i, id)
	}
	wg.Wait()

	merged := &msgpack.QueryResults{}
	var (
		ordered  bool
		firstErr error
		okCount  int
	)
	for i, id := range targets {
		o := outcomes[i]
		if o.err != nil {
			// 单个数据源失败（无该库权限、会话失效等）不应让整条查询失败。
			log.Printf("聚合查询在数据源 %s 上失败，已跳过: %v", id, o.err)
			if firstErr == nil {
				firstErr = o.err
			}
			continue
		}
		if o.ord {
			ordered = true
		}
		if merged.QueryTime < o.raw.QueryTime {
			merged.QueryTime = o.raw.QueryTime
		}
		merged.Results = append(merged.Results, o.raw.Results...)
		okCount++
	}
	if okCount == 0 {
		if firstErr != nil {
			return nil, ordered, firstErr
		}
		return nil, ordered, myError(1105, "所有数据源均未返回结果集")
	}
	log.Printf("聚合查询: %d/%d 个数据源返回结果, 合并 %d 个结果集",
		okCount, len(targets), len(merged.Results))
	return merged, ordered, nil
}

// execQueryRaw 执行查询（含断连重连 + 查询工单自动提交），返回原始 Yearning 结果。
// 第二个返回值为 true 表示本次触发了自动提交查询工单。
func (h *Handler) execQueryRaw(be *yearning.QuerySession, srcID, q, schema string) (*msgpack.QueryResults, bool, error) {
	res, err := be.Exec(q, schema)
	if err != nil {
		// 连接类错误：丢弃失效会话、重连并重试一次。
		if yearning.IsConnError(err) || be.IsDead() {
			log.Printf("查询[%s] 连接失效(%v)，重连后重试 (source_id=%s)", h.user, err, srcID)
			h.dropSession(srcID)
			nb, e2 := h.sessionFor(srcID)
			if e2 != nil {
				return nil, false, myError(1105, fmt.Sprintf("查询失败且重连失败: %v", e2))
			}
			res, err = nb.Exec(q, schema)
			if err != nil {
				log.Printf("查询[%s] 重连后仍失败: %v", h.user, err)
				// 新会话上发同一条 SQL 又立刻断连：确定性失败，多半是 SQL 超出
				// Yearning 服务端的消息大小限制，重试没有意义，给出明确提示。
				if yearning.IsConnError(err) && len(q) > largeSQLThreshold {
					return nil, false, myError(1105,
						fmt.Sprintf("SQL 过大（压缩后 %d 字符），Yearning 服务端拒绝处理并断开了连接，请缩短 SQL（如拆分大 IN 列表）", len(q)))
				}
				// 保留原始错误（%w），使上层仍能识别连接类错误并跳过该数据源。
				return nil, false, fmt.Errorf("重连后查询仍失败: %w", err)
			}
		} else {
			log.Printf("查询[%s] 执行失败: %v", h.user, err)
			return nil, false, fmt.Errorf("查询失败: %w", err)
		}
	}
	if res.Error != "" {
		log.Printf("查询[%s] Yearning 返回错误: %s", h.user, res.Error)
		return nil, false, myError(1105, res.Error)
	}
	if res.Status {
		log.Printf("查询[%s] 工单未批准，尝试自动提交查询工单 (source_id=%s)", h.user, srcID)
		if err := h.yc.EnsureQueryOrder(srcID); err != nil {
			log.Printf("自动提交查询工单失败: %v", err)
		}
		res2, err2 := be.Exec(q, schema)
		if err2 != nil {
			return nil, true, myError(1105, fmt.Sprintf("查询失败: %v", err2))
		}
		if res2 != nil && res2.Error != "" {
			return nil, true, myError(1105, res2.Error)
		}
		if res2 == nil || res2.Status {
			return nil, true, myError(1227, "已提交查询工单，请等待审批通过后再查询（审核开启时需 Yearning 审批人批准）")
		}
		if len(res2.Results) == 0 {
			return nil, true, myError(1105, "Yearning 未返回结果集")
		}
		return res2, true, nil
	}
	if len(res.Results) == 0 {
		return nil, false, myError(1105, "Yearning 未返回结果集")
	}
	return res, false, nil
}

// HandleFieldList 处理 COM_FIELD_LIST。
// DataGrip 补全主要靠 information_schema / SHOW（走 HandleQuery）；
// COM_FIELD_LIST 在标准客户端极少被使用，返回占位字段避免报错。
func (h *Handler) HandleFieldList(table string, fieldWildcard string) ([]*mysql.Field, error) {
	h.rec(&audit.Event{
		ConnID:   h.connIDLocked(),
		User:     h.user,
		Cmd:      audit.CmdFieldList,
		Kind:     string(classifier.KindFieldList),
		Decision: audit.DecisionLocalFake,
		SQL:      table + " " + fieldWildcard,
	})
	return []*mysql.Field{
		{Name: []byte(""), OrgName: []byte(""), Table: []byte(table),
			Charset: 33, Type: mysql.MYSQL_TYPE_VAR_STRING},
	}, nil
}

// HandleStmtPrepare 处理 COM_STMT_PREPARE：只读校验 + 记录语句。
func (h *Handler) HandleStmtPrepare(query string) (int, int, interface{}, error) {
	if err := classifier.CheckReadOnly(query); err != nil {
		h.rec(&audit.Event{
			ConnID:   h.connIDLocked(),
			User:     h.user,
			Cmd:      audit.CmdStmtPrepare,
			Kind:     string(classifier.Classify(query, "")),
			Decision: audit.DecisionRejected,
			SQL:      query,
			ErrCode:  errCodeOf(err),
			Error:    err.Error(),
		})
		return 0, 0, nil, err
	}
	h.rec(&audit.Event{
		ConnID:   h.connIDLocked(),
		User:     h.user,
		Cmd:      audit.CmdStmtPrepare,
		Kind:     string(classifier.Classify(query, "")),
		Decision: audit.DecisionPrepared,
		SQL:      query,
	})
	// 参数数量由占位符个数估算；列数未知先给 0。
	return strings.Count(query, "?"), 0, query, nil
}

// HandleStmtExecute 处理 COM_STMT_EXECUTE：参数内联后走 HandleQuery。
func (h *Handler) HandleStmtExecute(context interface{}, query string, args []interface{}) (*mysql.Result, error) {
	q := interpolate(query, args)
	return h.HandleQuery(q)
}

// HandleStmtClose 处理 COM_STMT_CLOSE。
func (h *Handler) HandleStmtClose(context interface{}) error {
	return nil
}

// HandleOtherCommand 拒绝未支持的命令。
func (h *Handler) HandleOtherCommand(cmd byte, data []byte) error {
	err := myError(1227, fmt.Sprintf("只读代理: 不支持的命令 0x%02x", cmd))
	// 计数这类命令（如 JDBC 的 COM_SET_OPTION）有助于判断客户端是否被降级。
	h.rec(&audit.Event{
		ConnID:   h.connIDLocked(),
		User:     h.user,
		Cmd:      audit.CmdName(cmd),
		Kind:     string(classifier.KindUnknown),
		Decision: audit.DecisionUnsupported,
		ErrCode:  errCodeOf(err),
		Error:    err.Error(),
	})
	return err
}

// Close 释放后端会话。
func (h *Handler) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for srcID, qs := range h.sessions {
		qs.Close()
		delete(h.sessions, srcID)
	}
}

// interpolate 把占位符参数转成文本并内联回 SQL（只读场景安全）。
func interpolate(query string, args []interface{}) string {
	for _, a := range args {
		query = strings.Replace(query, "?", literalFor(a), 1)
	}
	return query
}

// isLocalSessionStmt 判断语句是否属于「会话本地设置/事务控制」，
// 这类语句由代理本地消化，不转发给 Yearning 的查询引擎。
func isLocalSessionStmt(firstWord string) bool {
	switch firstWord {
	case "set":
		return true
	}
	return false
}

// literalFor 把参数转成 SQL 字面量。
func literalFor(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return "NULL"
	case []byte:
		return quote(string(t))
	case string:
		return quote(t)
	case bool:
		if t {
			return "1"
		}
		return "0"
	default:
		if n, ok := v.(int64); ok {
			return fmt.Sprintf("%d", n)
		}
		return quote(fmt.Sprintf("%v", v))
	}
}

func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// stringsToRows 把 []string 转成 [][]interface{}，供结果集构造。
func stringsToRows(names []string) [][]interface{} {
	rows := make([][]interface{}, len(names))
	for i, n := range names {
		rows[i] = []interface{}{n}
	}
	return rows
}

// extractQualifier 从 SQL 里提取 `库名.表名` 中的库名（限定符），
// 匹配 FROM/JOIN/UPDATE/INTO/ON 后的第一个 `xxx.yyy` 取 xxx，
// 但跳过 information_schema/mysql/performance_schema/sys 这类系统库限定符。
func extractQualifier(q string) string {
	up := strings.ToUpper(q)
	for _, kw := range []string{"FROM ", "JOIN ", "UPDATE ", "INTO ", "ON "} {
		if idx := strings.Index(up, kw); idx >= 0 {
			rest := strings.TrimSpace(q[idx+len(kw):])
			qual := readQualifier(rest)
			if qual != "" && isRealSchema(qual) {
				return qual
			}
		}
	}
	return ""
}

// readQualifier 读取形如 `db.table` / `db`.`table` 的表达式中 db 部分，
// 若无点号则返回空（非限定名）。
func readQualifier(s string) string {
	// 反引号包裹的首段：`db`.
	if strings.HasPrefix(s, "`") {
		end := strings.IndexByte(s[1:], '`')
		if end < 0 {
			return ""
		}
		first := s[1 : 1+end]
		rest := s[1+end+1:] // 跳过 ` 和 反引号
		rest = strings.TrimSpace(rest)
		if strings.HasPrefix(rest, ".") {
			return first
		}
		return ""
	}
	// 无引号：读第一个标识符，看是否后跟 .
	i := 0
	for i < len(s) && (isIdentChar(s[i])) {
		i++
	}
	if i == 0 || i >= len(s) || s[i] != '.' {
		return ""
	}
	return s[:i]
}

func isIdentChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') || c == '_'
}

// extractTargetSchema 从 SQL 里提取目标库名，用于跨数据源路由。支持：
//   - `WHERE xxx TABLE_SCHEMA = '库'` / `SCHEMA_NAME = '库'`（优先，信息明确）
//   - `SHOW TABLES/COLUMNS/... FROM 库`
//   - `库.表` 前缀（FROM/JOIN/UPDATE/INTO 后）
func extractTargetSchema(q string) string {
	// 1) information_schema 查询里的库名过滤条件（优先），覆盖 DataGrip 的多种写法：
	//      WHERE T.table_schema = '库'
	//      WHERE lower(table_schema) = '库'
	//      WHERE t.table_schema='库'        （无空格）
	//      WHERE table_schema in ('库')
	for _, m := range reSchemaFilter.FindAllStringSubmatch(q, -1) {
		for _, g := range m[1:] {
			if g != "" && isRealSchema(g) {
				return g
			}
		}
	}

	// 2) SHOW TABLES / SHOW COLUMNS / SHOW INDEX ... FROM 库
	//    注意形如 `SHOW COLUMNS FROM 表 FROM 库` 的语句有两个 FROM，库名在最后一个；
	//    若只有一个 FROM 且是列/索引类查询，那个标识符是表名而非库名，不应覆盖当前 schema。
	upper := strings.ToUpper(q)
	// 用 " 关键词"（前导空格）匹配，才能覆盖 SHOW FULL TABLES / SHOW FULL COLUMNS 这类带修饰的写法。
	isTableListShow := strings.HasPrefix(upper, "SHOW ") && strings.Contains(upper, " TABLES")
	isColOrIdxShow := strings.HasPrefix(upper, "SHOW ") &&
		(strings.Contains(upper, " COLUMNS") || strings.Contains(upper, " FIELDS") ||
			strings.Contains(upper, " INDEX") || strings.Contains(upper, " INDEXES") ||
			strings.Contains(upper, " KEYS"))
	if isTableListShow || isColOrIdxShow {
		first := strings.Index(upper, " FROM ")
		last := strings.LastIndex(upper, " FROM ")
		if last >= 0 {
			rest := strings.TrimSpace(q[last+len(" FROM "):])
			// 优先级：限定的 `库.表`
			if qual := readQualifier(rest); qual != "" && isRealSchema(qual) {
				return qual
			}
			// 列/索引查询只有一个 FROM 时，该标识符是表名（如 SHOW COLUMNS FROM t）
			if isColOrIdxShow && last == first {
				return ""
			}
			if ident := readIdent(rest); ident != "" && isRealSchema(ident) {
				return strings.Trim(ident, "`")
			}
		}
	}

	// 2.5) SHOW CREATE TABLE/VIEW `库`.表 / 库.表 —— DDL 展示，需据此路由到正确 source。
	if strings.Contains(upper, "SHOW CREATE TABLE") || strings.Contains(upper, "SHOW CREATE VIEW") {
		if idx := strings.Index(upper, "SHOW CREATE"); idx >= 0 {
			rest := strings.TrimSpace(q[idx+len("SHOW CREATE"):])
			// 跳过 TABLE/VIEW 关键字
			if f := strings.Fields(rest); len(f) > 0 &&
				(strings.EqualFold(f[0], "TABLE") || strings.EqualFold(f[0], "VIEW")) {
				rest = strings.TrimSpace(rest[len(f[0]):])
			}
			if qual := readQualifier(rest); qual != "" && isRealSchema(qual) {
				return qual
			}
		}
	}

	// 3) `库.表` 前缀
	return extractQualifier(q)
}

// isRealSchema 判断是否真实业务库名（排除系统库）。
func isRealSchema(s string) bool {
	switch strings.ToLower(s) {
	case "", "mysql", "information_schema", "performance_schema", "sys":
		return false
	}
	return true
}

// readQuoted 读取单引号/双引号/反引号包裹的字符串，返回内部值（无引号）。
func readQuoted(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	switch s[0] {
	case '\'', '"', '`':
		q := s[0]
		if end := strings.IndexByte(s[1:], q); end >= 0 {
			return s[1 : 1+end]
		}
		return strings.Trim(s, string(q))
	default:
		return readIdent(s)
	}
}

// readIdent 读取字符串开头的标识符（支持反引号包裹）。
func readIdent(s string) string {
	if strings.HasPrefix(s, "`") {
		if end := strings.Index(s[1:], "`"); end >= 0 {
			return s[1 : 1+end]
		}
		return s
	}
	i := 0
	for i < len(s) {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '.' {
			i++
			continue
		}
		break
	}
	return s[:i]
}

// 确保实现了 server.Handler 接口。
var _ server.Handler = (*Handler)(nil)
