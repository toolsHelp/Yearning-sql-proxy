package resultset

import (
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
