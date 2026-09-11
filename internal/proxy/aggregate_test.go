package proxy

import (
	"errors"
	"testing"

	"github.com/go-mysql-org/go-mysql/mysql"

	"github.com/toolsHelp/Yearning-sql-proxy/internal/msgpack"
)

func TestQueryError(t *testing.T) {
	// MySQL 错误原样透传（保留错误码）
	me := myError(1044, "未知数据源")
	if got := queryError(me); got != error(me) {
		t.Errorf("MySQL 错误应原样透传, got %v", got)
	}

	// 连接类错误不应把 websocket 原文抛给 DataGrip
	err := queryError(errors.New("websocket: close sent"))
	var target *mysql.MyError
	if !errors.As(err, &target) {
		t.Fatalf("连接类错误应转成 MySQL 错误, got %v", err)
	}
	if target.Code != 1105 {
		t.Errorf("错误码 = %d, want 1105", target.Code)
	}
	if target.Message == "websocket: close sent" {
		t.Errorf("不应把底层错误原文直接返回: %s", target.Message)
	}

	// nil 透传
	if got := queryError(nil); got != nil {
		t.Errorf("queryError(nil) 应为 nil, got %v", got)
	}
}

func TestKindClassificationForAggregateQueries(t *testing.T) {
	r := &msgpack.QueryResults{Results: []*msgpack.Query{
		{Field: []map[string]interface{}{{}, {}}, Data: []map[string]interface{}{{}}},
	}}
	if got := rawFieldCount(r); got != 2 {
		t.Errorf("rawFieldCount = %d, want 2", got)
	}
	if got := rawRowCount(r); got != 1 {
		t.Errorf("rawRowCount = %d, want 1", got)
	}
}
