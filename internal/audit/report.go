package audit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// KindStat 是某个请求类别的聚合结果。
type KindStat struct {
	Kind       string
	Total      int
	ByDecision map[Decision]int
	// Fwd 为转发到 Yearning 的次数（含触发建工单的）。
	Fwd    int
	P50MS  int64
	P95MS  int64
	Sample string
}

// FPStat 是某个 SQL 指纹的聚合结果。
type FPStat struct {
	FP        string
	Count     int
	Decisions map[Decision]int
	Sample    string
}

// ErrStat 是某类错误的聚合结果。
type ErrStat struct {
	ErrCode uint16
	Kind    string
	Count   int
	Sample  string
}

// Summary 是整份审计日志的聚合结果。
type Summary struct {
	Total    int
	Kinds    []KindStat
	TopFP    []FPStat
	Errors   []ErrStat
	CmdCount map[string]int
	// Timeline 按时间排序的前 N 条事件，用于观察客户端内省顺序。
	Timeline []Event
	// Ordered 触发自动建工单的次数。
	Ordered int
	// ConnCount 出现过的连接数。
	ConnCount int
}

// Aggregate 把事件列表聚合成 Summary。topFP/timelineN 控制 Top 列表长度。
func Aggregate(events []Event, topFP, timelineN int) Summary {
	s := Summary{CmdCount: map[string]int{}}
	kindMap := map[string]*KindStat{}
	fpMap := map[string]*FPStat{}
	errMap := map[uint16]*ErrStat{}
	conns := map[uint32]bool{}
	latencies := map[string][]int64{}

	for i := range events {
		e := &events[i]
		s.Total++
		s.CmdCount[e.Cmd]++
		conns[e.ConnID] = true
		if e.Decision == DecisionForwardedOrdered {
			s.Ordered++
		}

		ks := kindMap[e.Kind]
		if ks == nil {
			ks = &KindStat{Kind: e.Kind, ByDecision: map[Decision]int{}, Sample: e.SQL}
			kindMap[e.Kind] = ks
		}
		ks.Total++
		ks.ByDecision[e.Decision]++
		if e.Decision == DecisionForwarded || e.Decision == DecisionForwardedOrdered {
			ks.Fwd++
			latencies[e.Kind] = append(latencies[e.Kind], e.LatencyMS)
		}

		if e.FP != "" {
			fs := fpMap[e.FP]
			if fs == nil {
				fs = &FPStat{FP: e.FP, Decisions: map[Decision]int{}, Sample: e.SQL}
				fpMap[e.FP] = fs
			}
			fs.Count++
			fs.Decisions[e.Decision]++
		}

		if e.ErrCode != 0 {
			es := errMap[e.ErrCode]
			if es == nil {
				es = &ErrStat{ErrCode: e.ErrCode, Kind: e.Kind, Sample: e.Error}
				errMap[e.ErrCode] = es
			}
			es.Count++
		}
	}

	for k, ks := range kindMap {
		if l, ok := latencies[k]; ok {
			ks.P50MS = percentile(l, 50)
			ks.P95MS = percentile(l, 95)
		}
		s.Kinds = append(s.Kinds, *ks)
	}
	for _, fs := range fpMap {
		s.TopFP = append(s.TopFP, *fs)
	}
	for _, es := range errMap {
		s.Errors = append(s.Errors, *es)
	}
	sort.Slice(s.Kinds, func(i, j int) bool {
		if s.Kinds[i].Fwd != s.Kinds[j].Fwd {
			return s.Kinds[i].Fwd > s.Kinds[j].Fwd
		}
		return s.Kinds[i].Total > s.Kinds[j].Total
	})
	sort.Slice(s.TopFP, func(i, j int) bool { return s.TopFP[i].Count > s.TopFP[j].Count })
	sort.Slice(s.Errors, func(i, j int) bool { return s.Errors[i].Count > s.Errors[j].Count })
	if topFP > 0 && len(s.TopFP) > topFP {
		s.TopFP = s.TopFP[:topFP]
	}

	timeline := make([]Event, len(events))
	copy(timeline, events)
	sort.Slice(timeline, func(i, j int) bool {
		if timeline[i].ConnID != timeline[j].ConnID {
			return timeline[i].ConnID < timeline[j].ConnID
		}
		return timeline[i].Time.Before(timeline[j].Time)
	})
	if timelineN > 0 && len(timeline) > timelineN {
		timeline = timeline[:timelineN]
	}
	s.Timeline = timeline
	s.ConnCount = len(conns)
	return s
}

// percentile 取整数百分位（p 取值 0-100）。
func percentile(vals []int64, p int) int64 {
	if len(vals) == 0 {
		return 0
	}
	sorted := make([]int64, len(vals))
	copy(sorted, vals)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := len(sorted) * p / 100
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// ReadEvents 从 JSONL 读取事件，无法解析的行跳过。
func ReadEvents(r io.Reader) ([]Event, error) {
	var out []Event
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// decisionColumns 是主矩阵里展示的处理方式列（按阅读顺序）。
var decisionColumns = []Decision{
	DecisionLocalServed,
	DecisionLocalEmpty,
	DecisionLocalFake,
	DecisionCacheHit,
	DecisionForwarded,
	DecisionForwardedOrdered,
	DecisionLocalUse,
	DecisionLocalSession,
	DecisionPrepared,
	DecisionRejected,
	DecisionUnsupported,
	DecisionError,
}

var decisionHeaders = map[Decision]string{
	DecisionLocalServed:      "本地已支持",
	DecisionLocalEmpty:       "本地屏蔽",
	DecisionLocalFake:        "本地伪造",
	DecisionCacheHit:         "缓存命中",
	DecisionForwarded:        "转发Yearning",
	DecisionForwardedOrdered: "转发+建工单",
	DecisionLocalUse:         "本地USE",
	DecisionLocalSession:     "本地会话",
	DecisionPrepared:         "预处理",
	DecisionRejected:         "只读拦截",
	DecisionUnsupported:      "命令不支持",
	DecisionError:            "错误",
}

// RenderMarkdown 把 Summary 渲染成 markdown 报告。
func RenderMarkdown(s Summary) string {
	var b strings.Builder
	sec := 0
	heading := func(title string) {
		sec++
		fmt.Fprintf(&b, "## %d. %s\n\n", sec, title)
	}
	b.WriteString("# sql-relay 请求支持度矩阵\n\n")
	fmt.Fprintf(&b, "共 %d 条命令，%d 个连接，触发自动建工单 %d 次。\n\n", s.Total, s.ConnCount, s.Ordered)

	heading("类别 × 处理方式")
	b.WriteString("`转发Yearning` 列越大，说明该类 metadata 越依赖后端查询通道，是 Metadata Snapshot 的优先候选。\n\n")
	b.WriteString("| 类别 | 合计 |")
	for _, d := range decisionColumns {
		fmt.Fprintf(&b, " %s |", decisionHeaders[d])
	}
	b.WriteString(" 转发p50/p95(ms) |\n")
	b.WriteString("| --- | --: |")
	for range decisionColumns {
		b.WriteString(" --: |")
	}
	b.WriteString(" --: |\n")
	for _, k := range s.Kinds {
		fmt.Fprintf(&b, "| `%s` | %d |", k.Kind, k.Total)
		for _, d := range decisionColumns {
			fmt.Fprintf(&b, " %d |", k.ByDecision[d])
		}
		if k.Fwd > 0 {
			fmt.Fprintf(&b, " %d/%d |\n", k.P50MS, k.P95MS)
		} else {
			b.WriteString(" - |\n")
		}
	}

	b.WriteString("\n")
	heading("命令分布")
	b.WriteString("| 命令 | 次数 |\n| --- | --: |\n")
	cmds := make([]string, 0, len(s.CmdCount))
	for c := range s.CmdCount {
		cmds = append(cmds, c)
	}
	sort.Strings(cmds)
	for _, c := range cmds {
		fmt.Fprintf(&b, "| %s | %d |\n", c, s.CmdCount[c])
	}
	b.WriteString("\n> `COM_FIELD_LIST` 为 0 说明客户端（DataGrip）未走该协议取字段。\n")

	if len(s.TopFP) > 0 {
		b.WriteString("\n")
		heading("SQL 指纹 Top")
		b.WriteString("| 次数 | 处理方式 | SQL 样本 |\n| --: | --- | --- |\n")
		for _, f := range s.TopFP {
			ds := make([]string, 0, len(f.Decisions))
			for d, n := range f.Decisions {
				ds = append(ds, fmt.Sprintf("%s×%d", d, n))
			}
			sort.Strings(ds)
			fmt.Fprintf(&b, "| %d | %s | `%s` |\n", f.Count, strings.Join(ds, " "), escapeCell(f.Sample))
		}
	}

	if len(s.Errors) > 0 {
		b.WriteString("\n")
		heading("错误分布")
		b.WriteString("| 错误码 | 类别 | 次数 | 样例 |\n| --: | --- | --: | --- |\n")
		for _, e := range s.Errors {
			fmt.Fprintf(&b, "| %d | `%s` | %d | %s |\n", e.ErrCode, e.Kind, e.Count, escapeCell(e.Sample))
		}
	}

	if len(s.Timeline) > 0 {
		b.WriteString("\n")
		heading("连接时间线（前若干条）")
		b.WriteString("| # | 连接 | 命令 | 类别 | 处理方式 | 耗时ms | SQL |\n| --: | --: | --- | --- | --- | --: | --- |\n")
		for i, e := range s.Timeline {
			fmt.Fprintf(&b, "| %d | %d | %s | `%s` | %s | %d | `%s` |\n",
				i+1, e.ConnID, e.Cmd, e.Kind, e.Decision, e.LatencyMS, escapeCell(timelineSQL(e)))
		}
	}
	return b.String()
}

// timelineSQL 时间线里展示的语句：无 SQL 时（如 COM_INIT_DB）回退展示库名。
func timelineSQL(e Event) string {
	if e.SQL != "" {
		return e.SQL
	}
	if e.Schema != "" {
		return "USE " + e.Schema
	}
	return ""
}

// escapeCell 转义 markdown 表格里会破坏格式的字符。
func escapeCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	if len([]rune(s)) > 160 {
		s = string([]rune(s)[:160]) + "…"
	}
	return s
}
