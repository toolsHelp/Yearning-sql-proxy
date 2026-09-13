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

// Convert 把 queryResults 转成单个 *mysql.Result。
//
// 单结果集时直接转换。多结果集来自「聚合模式」——同一条 SQL 打到多个数据源，
// 每个数据源各返回一个结果集，需要按行级 UNION 合并成一个结果集后返回，
// 否则客户端只会看到第一个数据源的数据。
func Convert(res *msgpack.QueryResults) (*mysql.Result, error) {
	if res == nil || len(res.Results) == 0 {
		return nil, fmt.Errorf("查询结果为空")
	}
	if len(res.Results) == 1 {
		return convertQuery(res.Results[0])
	}
	return convertMerged(res.Results)
}

// convertMerged 把多个结果集按行级 UNION 合并成一个 *mysql.Result。
//
// 以第一个结果集为基准列结构，其余结果集按「列名」对齐后追加行（不按位置，
// 避免不同数据源的列顺序差异导致数据错位）。若某个结果集的列名序列与基准
// 不完全一致，则无法安全对齐——该结果集整体跳过，只用可对齐的结果集，
// 宁缺毋滥，避免向客户端返回错位的数据。
func convertMerged(results []*msgpack.Query) (*mysql.Result, error) {
	base := results[0]
	baseCols := Columns(base)
	if len(baseCols) == 0 {
		return nil, fmt.Errorf("查询结果为空")
	}
	want := names(baseCols)

	// 基准列名 → 该结果集 Data 行里的取值键。
	baseKeys := make([]string, len(baseCols))
	for i, c := range baseCols {
		baseKeys[i] = c.Key
	}

	data := rowsOf(base.Data, baseKeys)
	for _, q := range results[1:] {
		keys, ok := alignKeys(Columns(q), want)
		if !ok {
			continue
		}
		data = append(data, rowsOf(q.Data, keys)...)
	}
	return buildResult(want, data)
}

// alignKeys 把 cols 对齐到 want 列名序列，返回对应的取值键。
// 列名序列与 want 不一致时返回 ok=false（无法安全合并该结果集）。
func alignKeys(cols []column, want []string) ([]string, bool) {
	if len(cols) != len(want) {
		return nil, false
	}
	byName := make(map[string]string, len(cols))
	for _, c := range cols {
		if _, dup := byName[c.Name]; dup {
			// 同名列重复时按名对齐会产生歧义，放弃该结果集。
			return nil, false
		}
		byName[c.Name] = c.Key
	}
	keys := make([]string, len(want))
	for i, name := range want {
		key, ok := byName[name]
		if !ok {
			return nil, false
		}
		keys[i] = key
	}
	return keys, true
}

// rowsOf 按 keys 从 Yearning 的 Data 行 map 取值，缺失键记为 NULL。
func rowsOf(rows []map[string]interface{}, keys []string) [][]interface{} {
	out := make([][]interface{}, 0, len(rows))
	for _, rowMap := range rows {
		row := make([]interface{}, len(keys))
		for i, k := range keys {
			if v, ok := rowMap[k]; ok {
				row[i] = toBytes(v)
			} else {
				row[i] = nil
			}
		}
		out = append(out, row)
	}
	return out
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
	keys := make([]string, len(cols))
	for i, c := range cols {
		keys[i] = c.Key
	}
	return buildResult(names(cols), rowsOf(q.Data, keys))
}

// buildResult 按列名与行数据构造 *mysql.Result，统一处理空类型与 utf8 charset。
func buildResult(colNames []string, data [][]interface{}) (*mysql.Result, error) {
	rs, err := mysql.BuildSimpleTextResultset(colNames, data)
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
