package proxy

import (
	"sort"
	"strings"

	"github.com/go-mysql-org/go-mysql/mysql"
)

// utf8Charset 是 MySQL 协议的 utf8 charset id。
const utf8Charset = 33

// 本文件为客户端的「服务器特性探测」返回伪造结果。
//
// 背景：Yearning 后端用受限账号，SHOW VARIABLES / SELECT @@xx / VERSION() 这类探测
// 转发过去要么报错、要么无权限。此前统一返回空结果，导致 DataGrip 拿不到服务端版本，
// 按「未知版本」降级，表现为补全不全、提示「对象的内省级别低」。
// 这里按伪装版本返回一份最小可用的变量表，列名与真实 MySQL 一致。

// serverVars 返回伪造的服务器变量表（key 为去掉 @@/@@session./@@global. 后的变量名）。
func serverVars(version string) map[string]string {
	v := map[string]string{
		"version_comment":          "MySQL Community Server (GPL)",
		"version_compile_os":       "Linux",
		"version_compile_machine":  "x86_64",
		"innodb_version":           version,
		"auto_increment_increment": "1",
		"auto_increment_offset":    "1",
		"character_set_client":     "utf8mb4",
		"character_set_connection": "utf8mb4",
		"character_set_results":    "utf8mb4",
		"character_set_server":     "utf8mb4",
		"character_set_system":     "utf8",
		"character_set_database":   "utf8mb4",
		"character_set_filesystem": "binary",
		"collation_connection":     "utf8mb4_general_ci",
		"collation_server":         "utf8mb4_general_ci",
		"collation_database":       "utf8mb4_general_ci",
		"transaction_isolation":    "REPEATABLE-READ",
		"tx_isolation":             "REPEATABLE-READ",
		"transaction_read_only":    "0",
		"tx_read_only":             "0",
		"autocommit":               "1",
		"sql_mode":                 "STRICT_TRANS_TABLES",
		"sql_select_limit":         "18446744073709551615",
		"sql_auto_is_null":         "0",
		"sql_safe_updates":         "0",
		"lower_case_table_names":   "0",
		"max_allowed_packet":       "67108864",
		"net_write_timeout":        "60",
		"net_read_timeout":         "30",
		"wait_timeout":             "28800",
		"interactive_timeout":      "28800",
		"max_connections":          "1000",
		"max_user_connections":     "0",
		"max_prepared_stmt_count":  "16382",
		"group_concat_max_len":     "1024",
		"div_precision_increment":  "4",
		"innodb_lock_wait_timeout": "50",
		"lock_wait_timeout":        "31536000",
		"time_zone":                "SYSTEM",
		"system_time_zone":         "CST",
		"default_storage_engine":   "InnoDB",
		"storage_engine":           "InnoDB",
		"have_innodb":              "YES",
		// 代理不支持 SSL，明确告知客户端，避免它走 TLS 相关特性探测。
		"have_ssl":     "NO",
		"have_openssl": "NO",
		"port":         "3306",
	}
	v["version"] = version
	return v
}

// fakeProbeResult 尝试为服务器特性探测返回伪造结果。
// 返回 false 表示本函数不处理，调用方继续走原来的屏蔽/转发逻辑。
// 只处理两类：SHOW VARIABLES（含 LIKE），以及不含 FROM 的变量/版本/库名/用户探测。
func (h *Handler) fakeProbeResult(q, firstWord string) (*mysql.Result, bool) {
	lower := strings.ToLower(q)
	vars := serverVars(h.cfg.ServerVersion)

	if firstWord == "show" && (strings.Contains(lower, "show variables") ||
		strings.Contains(lower, "session variables") || strings.Contains(lower, "global variables")) {
		if r := varResultset(vars, likePattern(q)); r != nil {
			return r, true
		}
	}

	if firstWord == "select" && !strings.Contains(lower, " from ") {
		return h.probeSelectResult(q, vars)
	}
	return nil, false
}

// probeSelectResult 处理 SELECT @@x / VERSION() / DATABASE() 之类的探测。
// 任一输出列无法求值就整体放弃（返回 false），避免给出半截结果。
func (h *Handler) probeSelectResult(q string, vars map[string]string) (*mysql.Result, bool) {
	lower := strings.ToLower(q)
	sel := strings.Index(lower, "select")
	if sel < 0 {
		return nil, false
	}
	body := strings.TrimSpace(q[sel+len("select"):])

	h.mu.Lock()
	schema := h.schema
	h.mu.Unlock()

	var names []string
	var vals []string
	for _, expr := range splitSelectList(body) {
		if expr == "" {
			continue
		}
		name, expr := probeColumn(expr)
		v, ok := evalProbeExpr(expr, vars, schema, h.user)
		if !ok {
			return nil, false
		}
		names = append(names, name)
		vals = append(vals, v)
	}
	if len(names) == 0 {
		return nil, false
	}
	row := make([]interface{}, len(vals))
	for i, v := range vals {
		row[i] = v
	}
	rs, err := mysql.BuildSimpleTextResultset(names, [][]interface{}{row})
	if err != nil {
		return nil, false
	}
	setUTF8Charset(rs)
	return &mysql.Result{Status: 0x0002, Resultset: rs}, true
}

// probeColumn 拆分 SELECT 列表项，返回展示列名与待求值的表达式。
// 有 AS 别名时用别名；否则保留表达式原文（真实 MySQL 也用原文作列名，
// 例如 `SELECT @@session.transaction_isolation` 的列名就是这个串）。
func probeColumn(item string) (name, expr string) {
	item = strings.TrimSpace(item)
	lower := strings.ToLower(item)
	if i := strings.Index(lower, " as "); i >= 0 {
		alias := strings.TrimSpace(item[i+len(" as "):])
		return strings.Trim(alias, "`\""), strings.TrimSpace(item[:i])
	}
	return strings.Trim(item, "`"), strings.Trim(item, "`")
}

// evalProbeExpr 求单个探测表达式的值。ok 为 false 表示无法伪造该表达式。
func evalProbeExpr(expr string, vars map[string]string, schema, user string) (string, bool) {
	e := strings.ToLower(strings.TrimSpace(expr))
	switch {
	case strings.HasPrefix(e, "@@"):
		return vars[normalizeVarName(e)], true
	case e == "version()":
		return vars["version"], true
	case e == "database()" || e == "schema()":
		return schema, true
	case e == "user()" || e == "current_user()" || e == "session_user()" || e == "system_user()":
		return user + "@127.0.0.1", true
	case strings.Contains(e, "user()"):
		// DataGrip 用 left(user(), instr(concat(user(),'@'),'@')-1) 取用户名部分。
		return user, true
	default:
		return "", false
	}
}

// normalizeVarName 把 @@session.auto_increment_increment 归一化成 auto_increment_increment。
func normalizeVarName(e string) string {
	n := strings.TrimPrefix(e, "@@")
	n = strings.TrimPrefix(n, "session.")
	n = strings.TrimPrefix(n, "global.")
	n = strings.TrimPrefix(n, "local.")
	return n
}

// varResultset 构造 SHOW VARIABLES 的结果集（列名与 MySQL 一致）。
func varResultset(vars map[string]string, pattern string) *mysql.Result {
	names := make([]string, 0, len(vars))
	for k := range vars {
		names = append(names, k)
	}
	sort.Strings(names)

	rows := make([][]interface{}, 0, len(names))
	for _, k := range names {
		if pattern != "" && !likeMatch(k, pattern) {
			continue
		}
		rows = append(rows, []interface{}{k, vars[k]})
	}
	rs, err := mysql.BuildSimpleTextResultset([]string{"Variable_name", "Value"}, rows)
	if err != nil {
		return nil
	}
	setUTF8Charset(rs)
	return &mysql.Result{Status: 0x0002, Resultset: rs}
}

// setUTF8Charset 统一设置结果集字段的 charset（与代理其它结果集一致）。
func setUTF8Charset(rs *mysql.Resultset) {
	for _, f := range rs.Fields {
		f.Charset = utf8Charset
	}
}

// likePattern 提取 LIKE 'xxx' 里的模式，无 LIKE 时返回空串（表示不过滤）。
func likePattern(q string) string {
	lower := strings.ToLower(q)
	idx := strings.Index(lower, " like ")
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(q[idx+len(" like "):])
	s, ok := readStringLiteral(rest)
	if !ok {
		return ""
	}
	return strings.ToLower(s)
}

// readStringLiteral 读取开头的单/双引号字符串。
func readStringLiteral(s string) (string, bool) {
	if s == "" || (s[0] != '\'' && s[0] != '"') {
		return "", false
	}
	q := s[0]
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			i++
			b.WriteByte(s[i])
			continue
		}
		if c == q {
			return b.String(), true
		}
		b.WriteByte(c)
	}
	return "", false
}

// likeMatch 支持 % 与 _ 的简易 LIKE 匹配（够用于变量过滤）。
func likeMatch(s, pattern string) bool {
	if pattern == "%" {
		return true
	}
	parts := strings.Split(pattern, "%")
	if len(parts) == 1 {
		return simpleLikeMatch(s, pattern)
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	rest := s[len(parts[0]):]
	last := parts[len(parts)-1]
	for _, p := range parts[1 : len(parts)-1] {
		i := indexLike(rest, p)
		if i < 0 {
			return false
		}
		rest = rest[i+len(p):]
	}
	if last == "" {
		return true
	}
	return len(rest) >= len(last) && rest[len(rest)-len(last):] == last
}

// simpleLikeMatch 处理不含 % 的模式（_ 匹配单个字符）。
func simpleLikeMatch(s, pattern string) bool {
	if len(s) != len(pattern) {
		return false
	}
	for i := 0; i < len(s); i++ {
		if pattern[i] == '_' {
			continue
		}
		if s[i] != pattern[i] {
			return false
		}
	}
	return true
}

// indexLike 查找子串，_ 视为单字符通配。
func indexLike(s, sub string) int {
	if sub == "" {
		return 0
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		ok := true
		for j := 0; j < len(sub); j++ {
			if sub[j] != '_' && s[i+j] != sub[j] {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

// splitSelectList 按顶层逗号切分 SELECT 列表（括号/引号内的逗号不切）。
func splitSelectList(s string) []string {
	var out []string
	var quote byte
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' && i+1 < len(s) {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			quote = c
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	out = append(out, strings.TrimSpace(s[start:]))
	return out
}
