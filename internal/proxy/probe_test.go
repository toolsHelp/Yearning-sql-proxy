package proxy

import (
	"strings"
	"testing"

	"github.com/toolsHelp/Yearning-sql-proxy/internal/config"
)

// newProbeHandler 构造一个只用于测试伪造探测的 Handler。
func newProbeHandler(t *testing.T, schema, user string) *Handler {
	t.Helper()
	cfg := &config.Config{ServerVersion: "5.7.36"}
	h := NewHandler(cfg, nil, nil)
	h.SetUser(user)
	h.mu.Lock()
	h.schema = schema
	h.mu.Unlock()
	return h
}

// resultText 把结果集第一行拼成 "列=值" 便于断言。
func resultText(t *testing.T, h *Handler, q string) (string, bool) {
	t.Helper()
	r, ok := h.fakeProbeResult(q, firstWordOf(q))
	if !ok || r == nil || r.Resultset == nil {
		return "", false
	}
	var names []string
	for _, f := range r.Fields {
		names = append(names, string(f.Name))
	}
	if len(r.RowDatas) == 0 {
		return strings.Join(names, "|"), true
	}
	vals, err := r.RowDatas[0].Parse(r.Fields, false, nil)
	if err != nil {
		t.Fatalf("解析结果行失败: %v", err)
	}
	var out []string
	for i := range vals {
		out = append(out, string(vals[i].AsString()))
	}
	return strings.Join(names, "|") + " = " + strings.Join(out, "|"), true
}

func firstWordOf(q string) string {
	s := strings.ToLower(strings.TrimSpace(q))
	if i := strings.Index(s, " "); i > 0 {
		return s[:i]
	}
	return s
}

func TestFakeProbeVersionAndVars(t *testing.T) {
	h := newProbeHandler(t, "gaia_edi", "gaia_edi")

	cases := []struct {
		q    string
		want string
	}{
		{"SELECT @@version", "@@version = 5.7.36"},
		{"SELECT @@session.transaction_isolation", "@@session.transaction_isolation = REPEATABLE-READ"},
		{"SELECT @@session.transaction_read_only", "@@session.transaction_read_only = 0"},
		{"select version(), @@version_comment, database()",
			"version()|@@version_comment|database() = 5.7.36|MySQL Community Server (GPL)|gaia_edi"},
		{"select database()", "database() = gaia_edi"},
		{"select database(), schema(), left(user(),instr(concat(user(),'@'),'@')-1)",
			"database()|schema()|left(user(),instr(concat(user(),'@'),'@')-1) = gaia_edi|gaia_edi|gaia_edi"},
		// 带 AS 别名时列名取别名（mysql-connector-j 的写法）
		{"SELECT @@session.auto_increment_increment AS auto_increment_increment, @@character_set_client AS character_set_client",
			"auto_increment_increment|character_set_client = 1|utf8mb4"},
	}
	for _, c := range cases {
		got, ok := resultText(t, h, c.q)
		if !ok {
			t.Fatalf("fakeProbeResult(%q) 未伪造成功", c.q)
		}
		if got != c.want {
			t.Errorf("fakeProbeResult(%q) = %q, want %q", c.q, got, c.want)
		}
	}
}

func TestFakeProbeShowVariables(t *testing.T) {
	h := newProbeHandler(t, "gaia_edi", "gaia_edi")
	r, ok := h.fakeProbeResult("show variables like 'character_set_%'", "show")
	if !ok || r == nil {
		t.Fatalf("SHOW VARIABLES 应被伪造")
	}
	if len(r.Fields) != 2 {
		t.Fatalf("SHOW VARIABLES 应有 2 列，实际 %d", len(r.Fields))
	}
	if got := string(r.Fields[0].Name); got != "Variable_name" {
		t.Errorf("首列名 = %q, want Variable_name", got)
	}
	if len(r.RowDatas) == 0 {
		t.Fatalf("LIKE 'character_set_%%' 应至少匹配一行")
	}
	for _, rd := range r.RowDatas {
		vals, err := rd.Parse(r.Fields, false, nil)
		if err != nil {
			t.Fatalf("解析结果行失败: %v", err)
		}
		name := string(vals[0].AsString())
		if !strings.HasPrefix(name, "character_set_") {
			t.Errorf("LIKE 过滤失效，出现 %q", name)
		}
	}

	// 无 LIKE 时返回全部变量
	r2, _ := h.fakeProbeResult("SHOW VARIABLES", "show")
	if len(r2.RowDatas) < 10 {
		t.Errorf("SHOW VARIABLES 无 LIKE 时应返回全部变量，实际 %d 行", len(r2.RowDatas))
	}
}

func TestFakeProbeSkipsOthers(t *testing.T) {
	h := newProbeHandler(t, "gaia_edi", "gaia_edi")
	// 业务查询、带 FROM 的语句、非探测 SHOW 都不应被伪造
	for _, q := range []string{
		"SELECT * FROM gaia_edi.t_order",
		"SHOW STATUS",
		"SHOW ENGINES",
		"SHOW CREATE TABLE `gaia_edi`.`t`",
		"SHOW FULL TABLES FROM `gaia_edi`",
	} {
		if _, ok := h.fakeProbeResult(q, firstWordOf(q)); ok {
			t.Errorf("fakeProbeResult(%q) 不应伪造", q)
		}
	}
}

func TestLikeMatch(t *testing.T) {
	cases := []struct {
		s    string
		pat  string
		want bool
	}{
		{"character_set_server", "character_set_%", true},
		{"character_set_server", "collation%", false},
		{"version", "vers%", true},
		{"version", "%ion", true},
		{"version", "%", true},
		{"version", "vers_on", true},
		{"version", "versio_", true},
		{"version", "versioX", false},
		{"wait_timeout", "%timeout", true},
		{"wait_timeout", "wait%out", true},
	}
	for _, c := range cases {
		if got := likeMatch(c.s, c.pat); got != c.want {
			t.Errorf("likeMatch(%q, %q) = %v, want %v", c.s, c.pat, got, c.want)
		}
	}
}

func TestSplitSelectList(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"@@a, @@b", 2},
		{"left(user(),instr(concat(user(),'@'),'@')-1), database()", 2},
		{"version(), @@version_comment, database()", 3},
		{"'a,b', @@c", 2},
		{"@@a", 1},
	}
	for _, c := range cases {
		got := splitSelectList(c.in)
		if len(got) != c.want {
			t.Errorf("splitSelectList(%q) 得到 %d 段 %v, want %d", c.in, len(got), got, c.want)
		}
	}
}
