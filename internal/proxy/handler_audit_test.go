package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/toolsHelp/Yearning-sql-proxy/internal/audit"
	"github.com/toolsHelp/Yearning-sql-proxy/internal/config"
	"github.com/toolsHelp/Yearning-sql-proxy/internal/yearning"
)

// newAuditHandler 构造一个带审计日志的 Handler，后端用 httptest 模拟 Yearning 的
// 登录 / 数据源 / 库列表接口（不涉及 WebSocket，只能覆盖本地处理的分支）。
func newAuditHandler(t *testing.T) (*Handler, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/ldap"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"payload": map[string]string{"token": "tok"}, "code": 1200})
		case strings.HasPrefix(r.URL.Path, "/api/v2/fetch/source"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"payload": []map[string]interface{}{{"source": "src1", "source_id": "id1"}}, "code": 1200})
		case strings.HasPrefix(r.URL.Path, "/api/v2/query/schema"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"payload": []map[string]interface{}{{"key": "db1"}}, "code": 1200})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	cfg := &config.Config{Yearning: config.Yearning{
		BaseURL: srv.URL, LoginUser: "u", LoginPassword: "p", AuthMode: "ldap", IsLDAP: true}}
	cfg.Timeout.Connect = 5000000000 // 5s

	logPath := filepath.Join(t.TempDir(), "audit.jsonl")
	l, err := audit.Open(logPath, 512, true)
	if err != nil {
		t.Fatalf("打开审计日志失败: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })

	h := NewHandler(cfg, yearning.NewClient(cfg), nil)
	h.SetAudit(l)
	h.SetConnID(42)
	h.SetUser("src1")
	return h, logPath
}

func decisionsOf(t *testing.T, path string) map[string]audit.Decision {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("打开审计日志失败: %v", err)
	}
	defer f.Close()
	events, err := audit.ReadEvents(f)
	if err != nil {
		t.Fatalf("解析审计日志失败: %v", err)
	}
	out := make(map[string]audit.Decision, len(events))
	for _, e := range events {
		out[e.SQL] = e.Decision
	}
	return out
}

func TestAuditLocalDecisions(t *testing.T) {
	h, path := newAuditHandler(t)

	cases := []struct {
		sql  string
		want audit.Decision
	}{
		{"SET NAMES utf8mb4", audit.DecisionLocalSession},
		{"USE `db1`", audit.DecisionLocalUse},
		{"show databases", audit.DecisionLocalServed},
		// 服务器特性探测改为返回伪造值（此前是空结果）
		{"SHOW VARIABLES", audit.DecisionLocalServed},
		{"SELECT @@version", audit.DecisionLocalServed},
		{"select version(), @@version_comment, database()", audit.DecisionLocalServed},
		// 权限/管理类探测仍然本地屏蔽
		{"SHOW GRANTS", audit.DecisionLocalEmpty},
		{"UPDATE db1.t SET a = 1", audit.DecisionRejected},
	}
	for _, c := range cases {
		if _, err := h.HandleQuery(c.sql); err != nil {
			t.Logf("HandleQuery(%q) 返回错误（审计仍应记录）: %v", c.sql, err)
		}
	}

	got := decisionsOf(t, path)
	for _, c := range cases {
		d, ok := got[c.sql]
		if !ok {
			t.Fatalf("审计未记录 %q（已记录: %v）", c.sql, got)
		}
		if d != c.want {
			t.Errorf("HandleQuery(%q) 的 decision = %q, want %q", c.sql, d, c.want)
		}
	}
}

func TestAuditWriteIntercepted(t *testing.T) {
	h, path := newAuditHandler(t)
	if _, err := h.HandleQuery("DELETE FROM db1.t"); err == nil {
		t.Fatalf("写语句应被拦截")
	}
	got := decisionsOf(t, path)
	if got["DELETE FROM db1.t"] != audit.DecisionRejected {
		t.Errorf("写语句 decision = %q, want %q", got["DELETE FROM db1.t"], audit.DecisionRejected)
	}
}

func TestAuditFieldListAndOtherCommand(t *testing.T) {
	h, path := newAuditHandler(t)
	if _, err := h.HandleFieldList("t_order", "*"); err != nil {
		t.Fatalf("HandleFieldList 失败: %v", err)
	}
	if err := h.HandleOtherCommand(0x1b, nil); err == nil {
		t.Fatalf("未知命令应被拒绝")
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("打开审计日志失败: %v", err)
	}
	defer f.Close()
	events, err := audit.ReadEvents(f)
	if err != nil {
		t.Fatalf("解析审计日志失败: %v", err)
	}
	var field, other *audit.Event
	for i := range events {
		switch events[i].Cmd {
		case audit.CmdFieldList:
			field = &events[i]
		case "COM_OTHER:0x1b":
			other = &events[i]
		}
	}
	if field == nil {
		t.Fatalf("未记录 COM_FIELD_LIST")
	}
	if field.Decision != audit.DecisionLocalFake {
		t.Errorf("COM_FIELD_LIST decision = %q, want %q", field.Decision, audit.DecisionLocalFake)
	}
	if field.ConnID != 42 {
		t.Errorf("审计事件未带上 conn_id: %d", field.ConnID)
	}
	if other == nil {
		t.Fatalf("未记录未知命令")
	}
	if other.Decision != audit.DecisionUnsupported || other.ErrCode != 1227 {
		t.Errorf("未知命令审计不对: decision=%q errcode=%d", other.Decision, other.ErrCode)
	}
}

func TestResolveSourceUserAsSchema(t *testing.T) {
	// 常见误用：DataGrip 的 user 填成了库名而不是数据源名，应退一步按库名解析。
	h, _ := newAuditHandler(t)
	h.SetUser("db1")
	id, err := h.resolveSource("")
	if err != nil {
		t.Fatalf("user 填库名时应能解析: %v", err)
	}
	if id != "id1" {
		t.Errorf("resolveSource = %q, want %q", id, "id1")
	}
}

func TestResolveSourceAggregatesForUnknownUser(t *testing.T) {
	// 聚合模式：user 填了不认识的名字（含留空）也不应报错，而是自动选一个可用数据源，
	// 这样用户只配一个连接就能看到账号下有权限的所有库。
	for _, user := range []string{"nope", "", "order_db"} {
		h, _ := newAuditHandler(t)
		h.SetUser(user)
		id, err := h.resolveSource("")
		if err != nil {
			t.Fatalf("user=%q 时应聚合兜底而不是报错: %v", user, err)
		}
		if id == "" {
			t.Errorf("user=%q 时未选出数据源", user)
		}
	}
}

func TestTargetSources(t *testing.T) {
	h, _ := newAuditHandler(t)
	h.SetUser("nope")

	// SQL 带库名 → 只查该库所属数据源。
	// 注意此时 SchemaMap 尚未加载（懒加载），targetSources 必须主动加载一次，
	// 否则会把该库的查询兜底到别的数据源，导致查到 0 行。
	ids, err := h.targetSources("db1", "select * from information_schema.columns where table_schema='db1'")
	if err != nil {
		t.Fatalf("targetSources 失败: %v", err)
	}
	if len(ids) != 1 || ids[0] != "id1" {
		t.Errorf("按库名应定位到 id1, got %v", ids)
	}

	// 无库名的 information_schema 查询 → 跨数据源聚合
	ids, err = h.targetSources("", "select table_name from information_schema.tables")
	if err != nil {
		t.Fatalf("targetSources 失败: %v", err)
	}
	if len(ids) != 1 || ids[0] != "id1" {
		t.Errorf("单数据源时应返回该数据源, got %v", ids)
	}

	// 业务查询不带库名 → 只走兜底数据源，不做聚合
	if needsAllSources("select * from t") {
		t.Errorf("业务查询不应触发跨数据源聚合")
	}
	if !needsAllSources("select table_name from information_schema.tables") {
		t.Errorf("无库名的 information_schema 查询应触发跨数据源聚合")
	}
}

func TestAuditDisabledByDefault(t *testing.T) {
	// 未设置审计日志时，HandleQuery 不应 panic 也不应产生文件。
	cfg := &config.Config{Yearning: config.Yearning{BaseURL: "http://127.0.0.1:1"}}
	h := NewHandler(cfg, yearning.NewClient(cfg), nil)
	if _, err := h.HandleQuery("SET NAMES utf8mb4"); err != nil {
		t.Errorf("未启用审计时查询失败: %v", err)
	}
}
