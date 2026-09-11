package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/toolsHelp/Yearning-sql-proxy/internal/config"
)

func TestNewDisabled(t *testing.T) {
	// 未启用审计时返回 nil Logger，且 nil 上 Record 不应 panic。
	l, err := New(&config.Config{})
	if err != nil {
		t.Fatalf("New 返回错误: %v", err)
	}
	if l != nil {
		t.Fatalf("未启用审计时应返回 nil Logger")
	}
	l.Record(Event{SQL: "SELECT 1"})
	if err := l.Close(); err != nil {
		t.Fatalf("nil Logger Close 应返回 nil: %v", err)
	}
}

func TestRecordWritesJSONL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.jsonl")
	l, err := Open(path, 512, false)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	l.Record(Event{ConnID: 7, User: "gaia_edi", Cmd: CmdQuery, Kind: "column.info_columns",
		Decision: DecisionForwarded, SQL: "select * from information_schema.columns", Rows: 3, LatencyMS: 12})
	if err := l.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读回审计日志失败: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 1 {
		t.Fatalf("应有 1 行，实际 %d 行: %q", len(lines), string(b))
	}
	for _, want := range []string{`"kind":"column.info_columns"`, `"decision":"forwarded"`, `"conn_id":7`} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("审计行缺少 %s: %s", want, lines[0])
		}
	}
}

func TestRecordTruncatesAndMasks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.jsonl")
	l, err := Open(path, 20, false)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	l.Record(Event{Cmd: CmdQuery, Kind: "biz.select", Decision: DecisionForwarded,
		SQL: "SELECT * FROM t WHERE name = '0123456789abcdef' AND id = 123456789"})
	_ = l.Close()

	events, err := readFile(t, path)
	if err != nil {
		t.Fatalf("读回失败: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("应有 1 条事件，实际 %d", len(events))
	}
	got := events[0].SQL
	if !strings.Contains(got, "…(+") {
		t.Errorf("SQL 未被截断: %q", got)
	}
	if strings.Contains(got, "0123456789abcdef") || strings.Contains(got, "123456789") {
		t.Errorf("字面量未被脱敏: %q", got)
	}
	if !strings.Contains(events[0].FP, "?") {
		t.Errorf("指纹应含脱敏占位符: %q", events[0].FP)
	}
}

func TestRecordKeepLiterals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.jsonl")
	l, err := Open(path, 512, true)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	l.Record(Event{Cmd: CmdQuery, Kind: "biz.select", Decision: DecisionForwarded, SQL: "SELECT * FROM t WHERE id = 123456789"})
	_ = l.Close()

	events, err := readFile(t, path)
	if err != nil {
		t.Fatalf("读回失败: %v", err)
	}
	if !strings.Contains(events[0].SQL, "123456789") {
		t.Errorf("keep_literals=true 时应保留原文: %q", events[0].SQL)
	}
}

func TestCmdName(t *testing.T) {
	cases := []struct {
		cmd  byte
		want string
	}{
		{0x03, CmdQuery},
		{0x04, CmdFieldList},
		{0x02, CmdInitDB},
		{0x1b, "COM_OTHER:0x1b"},
	}
	for _, c := range cases {
		if got := CmdName(c.cmd); got != c.want {
			t.Errorf("CmdName(0x%02x) = %q, want %q", c.cmd, got, c.want)
		}
	}
}

func TestAggregateAndRender(t *testing.T) {
	events := []Event{
		{Time: time.Now(), ConnID: 1, Cmd: CmdQuery, Kind: "column.info_columns",
			Decision: DecisionForwarded, SQL: "select * from information_schema.columns", FP: "fp1", LatencyMS: 100},
		{Time: time.Now(), ConnID: 1, Cmd: CmdQuery, Kind: "column.info_columns",
			Decision: DecisionCacheHit, SQL: "select * from information_schema.columns", FP: "fp1", LatencyMS: 1},
		{Time: time.Now(), ConnID: 1, Cmd: CmdQuery, Kind: "probe.show_variables",
			Decision: DecisionLocalEmpty, SQL: "show variables", FP: "fp2"},
		{Time: time.Now(), ConnID: 2, Cmd: CmdFieldList, Kind: "column.info_columns",
			Decision: DecisionError, SQL: "", ErrCode: 1105, Error: "boom"},
	}
	s := Aggregate(events, 10, 10)
	if s.Total != 4 || s.ConnCount != 2 {
		t.Fatalf("聚合总数/连接数不对: total=%d conns=%d", s.Total, s.ConnCount)
	}
	if s.CmdCount[CmdFieldList] != 1 {
		t.Errorf("COM_FIELD_LIST 计数应为 1，实际 %d", s.CmdCount[CmdFieldList])
	}
	var col *KindStat
	for i := range s.Kinds {
		if s.Kinds[i].Kind == "column.info_columns" {
			col = &s.Kinds[i]
		}
	}
	if col == nil {
		t.Fatalf("缺少 column.info_columns 的聚合")
	}
	if col.Fwd != 1 || col.ByDecision[DecisionCacheHit] != 1 {
		t.Errorf("转发/缓存计数不对: fwd=%d cache=%d", col.Fwd, col.ByDecision[DecisionCacheHit])
	}
	if col.P50MS != 100 || col.P95MS != 100 {
		t.Errorf("转发耗时分位不对: p50=%d p95=%d", col.P50MS, col.P95MS)
	}
	if len(s.Errors) != 1 || s.Errors[0].ErrCode != 1105 {
		t.Errorf("错误聚合不对: %+v", s.Errors)
	}
	if len(s.Timeline) != 4 || s.Timeline[0].ConnID != 1 {
		t.Errorf("时间线排序不对: %+v", s.Timeline[:1])
	}

	md := RenderMarkdown(s)
	for _, want := range []string{"column.info_columns", "转发Yearning", "1105", "COM_FIELD_LIST"} {
		if !strings.Contains(md, want) {
			t.Errorf("报告缺少 %q", want)
		}
	}
}

func readFile(t *testing.T, path string) ([]Event, error) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ReadEvents(f)
}
