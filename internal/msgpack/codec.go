// Package codec 负责与 Yearning 查询 WebSocket 之间 msgpack 的编解码。
//
// 字段名与 Yearning 源码(src/handler/personal/query.go 与 impl.go)严格对齐，
// 请求为 QueryDeal.Ref（type/sql/schema 三键），响应为 queryResults。
package msgpack

import (
	"github.com/vmihailenco/msgpack/v5"
)

// 请求类型常量，与 Yearning QueryDeal.Ref.Type 语义一致。
const (
	TypeConn  = 0 // 连接/初始化
	TypeClose = 1 // 关闭
	// 注意：Yearning 实际查询消息里 type 固定为 4（ding-reboot 中 variables.put("type", 4)）。
	TypeQuery = 4
)

// QueryDeal 是发往 Yearning 的查询请求。
// Yearning 用裸 struct tag（无 tag 名），字段名即 msgpack key。
type QueryDeal struct {
	Type   int    `msgpack:"type"`
	SQL    string `msgpack:"sql"`
	Schema string `msgpack:"schema"`
}

// QueryResults 是 Yearning 返回的查询结果。
// 字段名与 Yearning queryResults 结构体的 msgpack tag 一致。
type QueryResults struct {
	Export    bool     `msgpack:"export"`
	Error     string   `msgpack:"error"`
	Results   []*Query `msgpack:"results"`
	QueryTime int      `msgpack:"query_time"`
	Status    bool     `msgpack:"status"`
	Heartbeat string   `msgpack:"heartbeat"`
	IsOnly    bool     `msgpack:"is_only"`
}

// Query 是单个结果集：Field 为字段元信息，Data 为数据行。
type Query struct {
	Field []map[string]interface{} `msgpack:"field"`
	Data  []map[string]interface{} `msgpack:"data"`
}

// MarshalQuery 编码一条查询消息为 msgpack 二进制。
func MarshalQuery(sql, schema string) ([]byte, error) {
	return msgpack.Marshal(&QueryDeal{Type: TypeQuery, SQL: sql, Schema: schema})
}

// UnmarshalResults 解码 Yearning 返回的结果集。
func UnmarshalResults(b []byte) (*QueryResults, error) {
	r := new(QueryResults)
	if err := msgpack.Unmarshal(b, r); err != nil {
		return nil, err
	}
	return r, nil
}
