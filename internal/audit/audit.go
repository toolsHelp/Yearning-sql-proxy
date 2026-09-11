// Package audit 记录代理收到的每条客户端命令及其实际处理路径，
// 用于抓取 DataGrip 等客户端真实发出的 metadata 请求，并生成
// 「请求类别 × 处理方式」支持度矩阵。
//
// 日志为本地 JSONL（每行一条 Event），默认关闭；写盘失败只记录一次并自动停用，
// 绝不阻断查询链路。
package audit

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-mysql-org/go-mysql/mysql"

	"github.com/toolsHelp/Yearning-sql-proxy/internal/config"
)

// 已知命令名（go-mysql 分发的命令）。
const (
	CmdQuery        = "COM_QUERY"
	CmdFieldList    = "COM_FIELD_LIST"
	CmdInitDB       = "COM_INIT_DB"
	CmdStmtPrepare  = "COM_STMT_PREPARE"
	CmdStmtExecute  = "COM_STMT_EXECUTE"
	CmdStmtClose    = "COM_STMT_CLOSE"
	CmdOtherPattern = "COM_OTHER:0x%02x"
)

// Decision 是代理最终选择的实际处理路径（而非"应该"怎么处理）。
type Decision string

const (
	// DecisionRejected 被只读校验拦截。
	DecisionRejected Decision = "rejected"
	// DecisionLocalUse USE 语句本地消化。
	DecisionLocalUse Decision = "local_use"
	// DecisionLocalSession SET 等会话语句本地消化。
	DecisionLocalSession Decision = "local_session"
	// DecisionLocalServed 本地构造了真实结果（如 SHOW DATABASES 汇总）。
	DecisionLocalServed Decision = "local_served"
	// DecisionLocalEmpty 本地屏蔽，返回空结果集。
	DecisionLocalEmpty Decision = "local_empty"
	// DecisionLocalFake 本地返回了占位/伪造内容（如 COM_FIELD_LIST 的假字段）。
	DecisionLocalFake Decision = "local_fake"
	// DecisionPrepared 预处理语句（COM_STMT_PREPARE），尚未真正执行。
	DecisionPrepared Decision = "prepared"
	// DecisionCacheHit 命中本地元数据缓存。
	DecisionCacheHit Decision = "cache_hit"
	// DecisionForwarded 转发 Yearning 执行。
	DecisionForwarded Decision = "forwarded"
	// DecisionForwardedOrdered 转发且触发了自动建工单。
	DecisionForwardedOrdered Decision = "forwarded_ordered"
	// DecisionError 执行失败。
	DecisionError Decision = "error"
	// DecisionUnsupported 命令不被支持（HandleOtherCommand 拒绝）。
	DecisionUnsupported Decision = "unsupported_cmd"
)

// Event 是一条命令的审计记录。
type Event struct {
	Time      time.Time `json:"ts"`
	ConnID    uint32    `json:"conn_id"`
	User      string    `json:"user"`
	Cmd       string    `json:"cmd"`
	Kind      string    `json:"kind"`
	Decision  Decision  `json:"decision"`
	Schema    string    `json:"schema,omitempty"`
	Source    string    `json:"source_id,omitempty"`
	SQL       string    `json:"sql,omitempty"`
	FP        string    `json:"fp,omitempty"`
	Rows      int       `json:"rows"`
	LatencyMS int64     `json:"latency_ms"`
	ErrCode   uint16    `json:"errcode,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// Logger 以追加方式把 Event 写成 JSONL。nil 指针可安全调用 Record。
type Logger struct {
	mu     sync.Mutex
	f      *os.File
	maxSQL int
	keep   bool
	broken bool
}

// New 按配置构造 Logger；未启用时返回 (nil, nil)。
func New(cfg *config.Config) (*Logger, error) {
	if cfg == nil || !cfg.Audit.Enabled {
		return nil, nil
	}
	return Open(cfg.Audit.Path, cfg.Audit.MaxSQL, cfg.Audit.KeepLiterals)
}

// Open 打开（必要时创建）审计日志文件并构造 Logger。
func Open(path string, maxSQL int, keepLiterals bool) (*Logger, error) {
	if maxSQL <= 0 {
		maxSQL = 512
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, fmt.Errorf("打开审计日志 %s 失败: %w", path, err)
	}
	log.Printf("审计日志已启用: %s (max_sql=%d keep_literals=%v)", path, maxSQL, keepLiterals)
	return &Logger{f: f, maxSQL: maxSQL, keep: keepLiterals}, nil
}

// Record 写入一条事件。Logger 为 nil 或已失效时静默返回。
func (l *Logger) Record(e Event) {
	if l == nil {
		return
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	if e.SQL != "" {
		if !l.keep {
			e.SQL = maskLiterals(e.SQL)
		}
		// 指纹基于完整 SQL（已脱敏）生成，避免被截断后聚合粒度失真。
		e.FP = Fingerprint(e.SQL, true)
		e.SQL = truncateRunes(e.SQL, l.maxSQL)
	}
	if e.Decision == "" {
		e.Decision = DecisionError
	}

	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	b = append(b, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil || l.broken {
		return
	}
	if _, err := l.f.Write(b); err != nil {
		log.Printf("写审计日志失败，已停用审计: %v", err)
		l.broken = true
	}
}

// Close 关闭日志文件，可重复调用。
func (l *Logger) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	f := l.f
	l.f = nil
	return f.Close()
}

// Fingerprint 生成聚合用的 SQL 指纹：归一化空白、转小写，
// keepLiterals 为 false 时对字面量脱敏。
func Fingerprint(sql string, keepLiterals bool) string {
	s := stripCommentsAndCollapse(sql)
	if !keepLiterals {
		s = maskLiterals(s)
	}
	return strings.ToLower(s)
}

// stripCommentsAndCollapse 去掉 -- / # 行注释与 /* */ 块注释，并压缩空白。
// 仅用于生成指纹，不做引号状态机（误判只会影响聚合粒度，不影响行为）。
func stripCommentsAndCollapse(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '-':
			if i+2 < len(s) && s[i+1] == '-' && s[i+2] == ' ' {
				for i < len(s) && s[i] != '\n' {
					i++
				}
				continue
			}
			b.WriteByte(s[i])
		case '#':
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case '/':
			if i+1 < len(s) && s[i+1] == '*' {
				i += 2
				for i+1 < len(s) && !(s[i] == '*' && s[i+1] == '/') {
					i++
				}
				i++
				continue
			}
			b.WriteByte(s[i])
		default:
			b.WriteByte(s[i])
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// maskLiterals 把长字符串字面量与长数字替换为 ?，避免业务数据落盘。
func maskLiterals(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		c := s[i]
		switch c {
		case '\'', '"':
			j := i + 1
			for j < len(s) {
				if s[j] == '\\' && j+1 < len(s) {
					j += 2
					continue
				}
				if s[j] == c {
					break
				}
				j++
			}
			if j >= len(s) { // 未闭合，原样输出剩余
				b.WriteString(s[i:])
				return b.String()
			}
			if j-i-1 > 8 {
				b.WriteByte(c)
				b.WriteByte('?')
				b.WriteByte(c)
			} else {
				b.WriteString(s[i : j+1])
			}
			i = j + 1
		default:
			if c >= '0' && c <= '9' {
				j := i
				for j < len(s) && s[j] >= '0' && s[j] <= '9' {
					j++
				}
				if j-i >= 6 {
					b.WriteByte('?')
				} else {
					b.WriteString(s[i:j])
				}
				i = j
				continue
			}
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// truncateRunes 按 rune 截断字符串，超长时追加 …(+N)。
func truncateRunes(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	rs := []rune(s)
	if len(rs) <= max {
		return s
	}
	return string(rs[:max]) + fmt.Sprintf("…(+%d)", len(rs)-max)
}

// CmdName 把 MySQL 命令字节转成可读名（未知命令带十六进制后缀）。
func CmdName(cmd byte) string {
	switch cmd {
	case mysql.COM_QUERY:
		return CmdQuery
	case mysql.COM_FIELD_LIST:
		return CmdFieldList
	case mysql.COM_INIT_DB:
		return CmdInitDB
	case mysql.COM_STMT_PREPARE:
		return CmdStmtPrepare
	case mysql.COM_STMT_EXECUTE:
		return CmdStmtExecute
	case mysql.COM_STMT_CLOSE:
		return CmdStmtClose
	default:
		return fmt.Sprintf(CmdOtherPattern, cmd)
	}
}
