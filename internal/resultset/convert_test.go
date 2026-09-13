package resultset

import (
	"strings"
	"testing"

	"github.com/toolsHelp/Yearning-sql-proxy/internal/msgpack"
)

func field(title, idx string) map[string]interface{} {
	return map[string]interface{}{"title": title, "dataIndex": idx}
}

func TestColumns_dedup(t *testing.T) {
	q := &msgpack.Query{
		Field: []map[string]interface{}{
			field("id", "id"),
			field("name", "name"),
			field("name", "name2"),
		},
	}
	cols := Columns(q)
	if len(cols) != 3 {
		t.Fatalf("列数 = %d, 期望 3", len(cols))
	}
	if cols[0].Name != "id" || cols[0].Key != "id" {
		t.Fatalf("col0 = %+v", cols[0])
	}
	if cols[1].Name != "name" || cols[2].Name != "name(1)" {
		t.Fatalf("去重失败: %s / %s", cols[1].Name, cols[2].Name)
	}
	if cols[2].Key != "name2" {
		t.Fatalf("col2 key = %s, 期望 name2", cols[2].Key)
	}
}

func TestConvert(t *testing.T) {
	res := &msgpack.QueryResults{
		Results: []*msgpack.Query{{
			Field: []map[string]interface{}{
				field("id", "id"),
				field("name", "name"),
			},
			Data: []map[string]interface{}{
				{"id": "1", "name": "alice"},
				{"id": "2", "name": "bob"},
			},
		}},
	}
	r, err := Convert(res)
	if err != nil {
		t.Fatal(err)
	}
	if r.Resultset == nil {
		t.Fatal("Resultset 为空")
	}
	if len(r.Resultset.Fields) != 2 {
		t.Fatalf("Fields = %d, 期望 2", len(r.Resultset.Fields))
	}
	if len(r.Resultset.RowDatas) != 2 {
		t.Fatalf("RowDatas 行数 = %d, 期望 2", len(r.Resultset.RowDatas))
	}
	if string(r.Resultset.Fields[0].Name) != "id" {
		t.Fatalf("首列名 = %q, 期望 id", r.Resultset.Fields[0].Name)
	}
}

func TestConvert_missingKey_isNull(t *testing.T) {
	res := &msgpack.QueryResults{
		Results: []*msgpack.Query{{
			Field: []map[string]interface{}{field("a", "a"), field("b", "b")},
			Data:  []map[string]interface{}{{"a": "1"}}, // 缺 b
		}},
	}
	r, err := Convert(res)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Resultset.RowDatas) != 1 {
		t.Fatalf("RowDatas 行数 = %d, 期望 1", len(r.Resultset.RowDatas))
	}
	// 第二列缺失应编码为 NULL(0xfb)。
	row := r.Resultset.RowDatas[0]
	if len(row) == 0 {
		t.Fatal("行数据为空")
	}
}

// TestConvert_mergesMultipleResults 验证聚合模式：多结果集按行级 UNION 合并。
func TestConvert_mergesMultipleResults(t *testing.T) {
	res := &msgpack.QueryResults{Results: []*msgpack.Query{
		{
			Field: []map[string]interface{}{field("TABLE_NAME", "TABLE_NAME")},
			Data: []map[string]interface{}{
				{"TABLE_NAME": "t1"},
				{"TABLE_NAME": "t2"},
			},
		},
		{
			Field: []map[string]interface{}{field("TABLE_NAME", "TABLE_NAME")},
			Data: []map[string]interface{}{
				{"TABLE_NAME": "t3"},
			},
		},
	}}
	r, err := Convert(res)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(r.Resultset.Fields); got != 1 {
		t.Fatalf("列数 = %d, 期望 1", got)
	}
	if got := len(r.Resultset.RowDatas); got != 3 {
		t.Fatalf("合并后行数 = %d, 期望 3（2+1）", got)
	}
}

// TestConvert_alignsByColumnName 验证列顺序不同时按列名对齐，而非按位置。
func TestConvert_alignsByColumnName(t *testing.T) {
	res := &msgpack.QueryResults{Results: []*msgpack.Query{
		{
			Field: []map[string]interface{}{field("a", "a"), field("b", "b")},
			Data:  []map[string]interface{}{{"a": "a1", "b": "b1"}},
		},
		{
			// 列顺序颠倒：若按位置合并会把 b2 塞进 a 列。
			Field: []map[string]interface{}{field("b", "b"), field("a", "a")},
			Data:  []map[string]interface{}{{"a": "a2", "b": "b2"}},
		},
	}}
	r, err := Convert(res)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(r.Resultset.RowDatas); got != 2 {
		t.Fatalf("行数 = %d, 期望 2", got)
	}
	// 第二行应仍是 a2/b2（按列名对齐），不是 b2/a2。
	row2 := r.Resultset.RowDatas[1]
	if !strings.Contains(string(row2), "a2") || !strings.Contains(string(row2), "b2") {
		t.Fatalf("第二行内容异常: %q", row2)
	}
	if strings.Index(string(row2), "a2") > strings.Index(string(row2), "b2") {
		t.Fatalf("列顺序错位，a 列不应排在 b 列之后: %q", row2)
	}
}

// TestConvert_skipsMismatchedColumns 验证列结构不一致的结果集被整体跳过。
func TestConvert_skipsMismatchedColumns(t *testing.T) {
	res := &msgpack.QueryResults{Results: []*msgpack.Query{
		{
			Field: []map[string]interface{}{field("a", "a")},
			Data:  []map[string]interface{}{{"a": "keep"}},
		},
		{
			Field: []map[string]interface{}{field("x", "x"), field("y", "y")},
			Data:  []map[string]interface{}{{"x": "drop", "y": "drop"}},
		},
	}}
	r, err := Convert(res)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(r.Resultset.RowDatas); got != 1 {
		t.Fatalf("列不匹配的结果集应被跳过，行数 = %d, 期望 1", got)
	}
}

// TestConvert_mergeKeepsEmptyRows 验证某些结果集无数据时仍保留基准列结构。
func TestConvert_mergeKeepsEmptyRows(t *testing.T) {
	res := &msgpack.QueryResults{Results: []*msgpack.Query{
		{
			Field: []map[string]interface{}{field("a", "a")},
			Data:  []map[string]interface{}{},
		},
		{
			Field: []map[string]interface{}{field("a", "a")},
			Data:  []map[string]interface{}{{"a": "v"}},
		},
	}}
	r, err := Convert(res)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(r.Resultset.Fields); got != 1 {
		t.Fatalf("列数 = %d, 期望 1", got)
	}
	if got := len(r.Resultset.RowDatas); got != 1 {
		t.Fatalf("行数 = %d, 期望 1", got)
	}
}
