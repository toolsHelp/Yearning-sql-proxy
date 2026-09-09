// Package classifier 负责把客户端发来的 SQL 拆分为语句并做只读判定。
//
// 核心是 stripComments（带引号状态机），保证字符串字面量内的注释符不回误判，
// 避免 /* x */ UPDATE 之类的伪装绕过。写语句会被拒绝，只读语句放行透传。
package classifier

import (
	"strings"

	"github.com/go-mysql-org/go-mysql/mysql"
)

// errDenied 使用 MySQL 的 ER_SPECIFIC_ACCESS_DENIED_ERROR(1227)。
const errDenied = 1227

const deniedMsg = "只读代理：变更语句请在 Yearning 提交工单执行"

// Denied 构造一条只读拦截错误。
func Denied(msg string) error {
	if msg == "" {
		msg = deniedMsg
	}
	return mysql.NewError(errDenied, msg)
}

// writeVerbs 是需要拦截的写/DDL 动词（可见计划.md）。
var writeVerbs = map[string]bool{
	"insert": true, "update": true, "delete": true, "replace": true,
	"create": true, "alter": true, "drop": true, "truncate": true,
	"rename": true, "load": true, "call": true, "grant": true,
	"revoke": true, "lock": true, "optimize": true, "analyze": true,
	"repair": true, "merge": true, "do": true, "handler": true,
	"install": true, "uninstall": true, "purge": true, "reset": true,
	"stop": true, "start": true, "shutdown": true, "kill": true,
	"backup": true, "restore": true, "import": true, "check": true,
	"checksum": true, "flush": true, "cache": true,
}

// readOnlyVerbs 是明确放行的只读/会话控制动词。
var readOnlyVerbs = map[string]bool{
	"select": true, "show": true, "desc": true, "describe": true,
	"explain": true, "use": true, "set": true,
	"begin": true, "start": true, "commit": true, "rollback": true,
	"savepoint": true, "release": true, "with": true, "values": true,
	"table": true, "checks": true,
}

// stripComments 去掉 SQL 中的注释，维护单引号/双引号/反引号状态机，
// 使字符串字面量内的 '--'、'#'、'/*' 不被当作注释。
func stripComments(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	var quote byte // 0 表示不在引号内
	var lineComment, blockComment bool
	for i := 0; i < len(s); i++ {
		c := s[i]
		if lineComment {
			if c == '\n' {
				lineComment = false
				b.WriteByte('\n')
			}
			continue
		}
		if blockComment {
			if c == '*' && i+1 < len(s) && s[i+1] == '/' {
				blockComment = false
				i++
			}
			continue
		}
		if quote != 0 {
			b.WriteByte(c)
			// 处理转义：反斜杠跳过下一字符
			if c == '\\' && i+1 < len(s) {
				i++
				b.WriteByte(s[i])
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
			b.WriteByte(c)
		case '-':
			if i+1 < len(s) && s[i+1] == '-' && (i+2 >= len(s) || s[i+2] == ' ' || s[i+2] == '\t') {
				lineComment = true
				i++
			} else {
				b.WriteByte(c)
			}
		case '#':
			lineComment = true
		case '/':
			if i+1 < len(s) && s[i+1] == '*' {
				blockComment = true
				i++
			} else {
				b.WriteByte(c)
			}
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// SplitStatements 按分号切分多语句，忽略注释与空语句。
func SplitStatements(q string) []string {
	var out []string
	for _, s := range strings.Split(q, ";") {
		s = strings.TrimSpace(stripComments(s))
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// FirstWord 返回语句剥离注释后的小写首词（供 USE 判定等复用）。
func FirstWord(stmt string) string {
	s := stripComments(stmt)
	fields := strings.Fields(strings.TrimSpace(s))
	if len(fields) == 0 {
		return ""
	}
	return strings.ToLower(fields[0])
}

// hasInToken 判断语句（已大写）中是否出现某个顶层关键字（忽略注释）。
func containsWord(upper, word string) bool {
	// 简化判断：检测 " word " 边界，覆盖前导/结尾边界。
	s := stripComments(strings.ToUpper(upper))
	return strings.Contains(" "+s+" ", " "+word+" ")
}

// CheckReadOnly 对整段查询做只读校验。任一语句违规即返回错误。
func CheckReadOnly(query string) error {
	stmts := SplitStatements(query)
	if len(stmts) == 0 {
		return nil
	}
	for _, stmt := range stmts {
		if err := checkOne(stmt); err != nil {
			return err
		}
	}
	return nil
}

func checkOne(stmt string) error {
	verb := FirstWord(stmt)
	switch {
	case verb == "select":
		up := strings.ToUpper(stripComments(stmt))
		if strings.Contains(up, "INTO OUTFILE") || strings.Contains(up, "INTO DUMPFILE") {
			return Denied("只读代理: SELECT ... INTO 被禁止")
		}
		// 锁定类 SELECT 也拒绝，避免写锁副作用。
		if strings.Contains(up, " FOR UPDATE") || strings.Contains(up, " LOCK IN SHARE MODE") {
			return Denied("只读代理: SELECT 加锁语句被禁止")
		}
	case verb == "with":
		// CTE：识别内嵌的写动作。
		up := strings.ToUpper(stripComments(stmt))
		for _, bad := range []string{"UPDATE", "DELETE", "INSERT", "REPLACE"} {
			if containsWord(up, bad) {
				return Denied("")
			}
		}
	case readOnlyVerbs[verb]:
		// 放行（含 information_schema/mysql/performance_schema/sys 探测）
	default:
		if writeVerbs[verb] {
			return Denied("只读代理: " + strings.ToUpper(verb) + " 被禁止, 请到 Yearning 提交工单")
		}
		return Denied("只读代理: 不支持的语句类型 " + strings.ToUpper(verb))
	}
	return nil
}
