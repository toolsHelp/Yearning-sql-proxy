package msgpack

import (
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func TestMarshalQuery_fields(t *testing.T) {
	b, err := MarshalQuery("select * from t", "mydb")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	if err := msgpack.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	// msgpack 对小整数可能解码为 int8/int16/int32/int64，做数值比较。
	switch typ := m["type"].(type) {
	case int8:
		if int(typ) != TypeQuery {
			t.Errorf("type = %v (%T), 期望 %d", m["type"], m["type"], TypeQuery)
		}
	case int16:
		if int(typ) != TypeQuery {
			t.Errorf("type = %v (%T), 期望 %d", m["type"], m["type"], TypeQuery)
		}
	case int32:
		if int(typ) != TypeQuery {
			t.Errorf("type = %v (%T), 期望 %d", m["type"], m["type"], TypeQuery)
		}
	case int64:
		if int(typ) != TypeQuery {
			t.Errorf("type = %v (%T), 期望 %d", m["type"], m["type"], TypeQuery)
		}
	case uint64:
		if int(typ) != TypeQuery {
			t.Errorf("type = %v (%T), 期望 %d", m["type"], m["type"], TypeQuery)
		}
	default:
		t.Errorf("type = %v (%T), 期望 %d", m["type"], m["type"], TypeQuery)
	}
	if m["sql"] != "select * from t" {
		t.Errorf("sql = %v", m["sql"])
	}
	if m["schema"] != "mydb" {
		t.Errorf("schema = %v", m["schema"])
	}
}

func TestUnmarshalResults(t *testing.T) {
	// 构造一条与 Yearning queryResults 同构的 msgpack。
	raw := map[string]interface{}{
		"export":     false,
		"error":      "",
		"query_time": 12,
		"status":     false,
		"heartbeat":  "",
		"is_only":    false,
		"results": []interface{}{
			map[string]interface{}{
				"field": []interface{}{
					map[string]interface{}{"title": "id", "dataIndex": "id"},
					map[string]interface{}{"title": "name", "dataIndex": "name"},
				},
				"data": []interface{}{
					map[string]interface{}{"id": "1", "name": "alice"},
					map[string]interface{}{"id": "2", "name": "bob"},
				},
			},
		},
	}
	b, err := msgpack.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	res, err := UnmarshalResults(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 1 {
		t.Fatalf("results 数量 = %d, 期望 1", len(res.Results))
	}
	q := res.Results[0]
	if len(q.Field) != 2 || len(q.Data) != 2 {
		t.Fatalf("field=%d data=%d, 期望 2/2", len(q.Field), len(q.Data))
	}
	if q.Field[0]["title"] != "id" || q.Data[0]["name"] != "alice" {
		t.Fatalf("解析结果不符: %v / %v", q.Field[0], q.Data[0])
	}
}
