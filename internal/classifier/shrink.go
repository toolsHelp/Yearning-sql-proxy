package classifier

import "strings"

// ShrinkSQL 压缩 SQL 以减小传输体积：
//   - 去掉行注释与块注释；
//   - 引号外的连续空白（空格/tab/换行/缩进）压成单个空格；
//   - 字符串字面量内容原样保留。
//
// 压缩后的 SQL 与原 SQL 词法等价。用途：Yearning 的查询 WebSocket 对单条
// 消息有大小限制（实测 55KB 会被服务端直接断连），DataGrip 格式化出的
// 长 SQL 里缩进换行占比很高，压缩后通常能降 30%~60%。
func ShrinkSQL(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	var quote byte           // 0 表示不在引号内
	var inLine, inBlock bool // 行注释 / 块注释状态
	spacePending := false    // 引号外有待输出的空白

	flushSpace := func() {
		if spacePending {
			b.WriteByte(' ')
			spacePending = false
		}
	}

	for i := 0; i < len(s); i++ {
		c := s[i]
		if inLine {
			if c == '\n' {
				inLine = false
				spacePending = true
			}
			continue
		}
		if inBlock {
			if c == '*' && i+1 < len(s) && s[i+1] == '/' {
				inBlock = false
				i++
				spacePending = true
			}
			continue
		}
		if quote != 0 {
			b.WriteByte(c)
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
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			spacePending = true
		case c == '-':
			if i+1 < len(s) && s[i+1] == '-' && (i+2 >= len(s) || s[i+2] == ' ' || s[i+2] == '\t') {
				inLine = true
				i++
				continue
			}
			flushSpace()
			b.WriteByte(c)
		case c == '#':
			inLine = true
		case c == '/':
			if i+1 < len(s) && s[i+1] == '*' {
				inBlock = true
				i++
				continue
			}
			flushSpace()
			b.WriteByte(c)
		case c == '\'' || c == '"' || c == '`':
			flushSpace()
			quote = c
			b.WriteByte(c)
		default:
			flushSpace()
			b.WriteByte(c)
		}
	}
	return strings.TrimSpace(b.String())
}
