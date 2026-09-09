// Package resultset 把 Yearning 返回的 queryResults 转成 go-mysql 的 *mysql.Result。
//
// Yearning 未下发真实列类型（值已字符串化），v0.1 全部映射为
// MYSQL_TYPE_VAR_STRING，保证 DataGrip 正常显示。
package resultset

import (
	"fmt"

	"github.com/go-mysql-org/go-mysql/mysql"

	"github.com/toolsHelp/Yearning-sql-proxy/internal/msgpack"
)

// utf8Charset 是 MySQL 协议的 utf8 charset id。
const utf8Charset = 33

// column 描述一个输出列：Name 为展示名，Key 为 data 行内取值键。
type column struct {
	Name string
	Key  string
}

// Convert 把 queryResults 的首个结果集转成 *mysql.Result。
// 多结果集时只取第一个（DataGrip 默认单语句）。
func Convert(res *msgpack.QueryResults) (*mysql.Result, error) {
	if res == nil || len(res.Results) == 0 {
		return nil, fmt.Errorf("查询结果为空")
	}
	return convertQuery(res.Results[0])
}

// Columns 解析结果集字段：展示名（去重加序号）与行取值键。
func Columns(q *msgpack.Query) []column {
	var out []column
	seen := map[string]int{}
	for i, f := range q.Field {
		title, _ := f["title"].(string)
		if title == "" {
			title, _ = f["dataIndex"].(string)
		}
		if title == "" {
			title = fmt.Sprintf("col_%d", i)
		}
		key, _ := f["dataIndex"].(string)
		if key == "" {
			key = title
		}
		seen[title]++
		name := title
		if seen[title] > 1 {
			name = fmt.Sprintf("%s(%d)", title, seen[title]-1)
		}
		out = append(out, column{Name: name, Key: key})
	}
	return out
}

func convertQuery(q *msgpack.Query) (*mysql.Result, error) {
	cols := Columns(q)

	var data [][]interface{}
	for _, rowMap := range q.Data {
		row := make([]interface{}, len(cols))
		for i := range cols {
			if v, ok := rowMap[cols[i].Key]; ok {
				row[i] = toBytes(v)
			} else {
				row[i] = nil
			}
		}
		data = append(data, row)
	}

	rs, err := mysql.BuildSimpleTextResultset(names(cols), data)
	if err != nil {
		return nil, err
	}
	// Yearning 值已字符串化，统一用 utf8 文本类型（Build* 已按值推导）。
	for _, f := range rs.Fields {
		if f.Type == mysql.MYSQL_TYPE_NULL {
			f.Type = mysql.MYSQL_TYPE_VAR_STRING
		}
		f.Charset = utf8Charset
	}
	return &mysql.Result{
		Status:    0x0002, // SERVER_STATUS_AUTOCOMMIT
		Resultset: rs,
	}, nil
}

func names(cols []column) []string {
	out := make([]string, len(cols))
	for i := range cols {
		out[i] = cols[i].Name
	}
	return out
}

// toBytes 把 Yearning 的标量值统一转成 []byte（nil 表示 NULL）。
func toBytes(v interface{}) []byte {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		return t
	case string:
		return []byte(t)
	case bool:
		if t {
			return []byte("true")
		}
		return []byte("false")
	default:
		return []byte(fmt.Sprintf("%v", t))
	}
}
