package classifier

import (
	"strings"
	"testing"
)

func TestStripComments(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"select 1 -- comment", "select 1 "},
		{"select 1 # comment", "select 1 "},
		{"select /* block */ 1", "select  1"},
		{"select '-- not comment'", "select '-- not comment'"},
		{`select "a--b"`, `select "a--b"`},
		{"select `x--y`", "select `x--y`"},
		{"select 1 /*x*/ UPDATE t", "select 1  UPDATE t"},
		{`select 'it''s'`, `select 'it''s'`}, // 字符串内转义引号
	}
	for _, c := range cases {
		got := stripComments(c.in)
		if got != c.want {
			t.Errorf("stripComments(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSplitStatements(t *testing.T) {
	got := SplitStatements("select 1; ; select 2; -- comment\nselect 3;")
	if len(got) != 3 {
		t.Fatalf("SplitStatements 返回 %d 条, 期望 3: %v", len(got), got)
	}
	if got[0] != "select 1" || got[1] != "select 2" || got[2] != "select 3" {
		t.Fatalf("SplitStatements 结果不符: %v", got)
	}
}

func TestCheckReadOnly_allow(t *testing.T) {
	allowed := []string{
		"SELECT * FROM t",
		"select * from information_schema.tables",
		"SHOW TABLES",
		"DESC t",
		"DESCRIBE t",
		"EXPLAIN SELECT 1",
		"USE mydb",
		"SET NAMES utf8mb4",
		"BEGIN",
		"START TRANSACTION",
		"COMMIT",
		"ROLLBACK",
		"select * from mysql.user",
		"select * from performance_schema.events_statements_summary_by_digest",
	}
	for _, q := range allowed {
		if err := CheckReadOnly(q); err != nil {
			t.Errorf("CheckReadOnly(%q) 不应拒绝, 得到 %v", q, err)
		}
	}
}

func TestCheckReadOnly_deny(t *testing.T) {
	denied := []string{
		"INSERT INTO t VALUES (1)",
		"UPDATE t SET a=1",
		"DELETE FROM t",
		"REPLACE INTO t VALUES (1)",
		"CREATE TABLE t (a int)",
		"ALTER TABLE t ADD COLUMN b int",
		"DROP TABLE t",
		"TRUNCATE TABLE t",
		"RENAME TABLE t TO t2",
		"LOAD DATA INFILE 'x'",
		"CALL proc()",
		"GRANT SELECT ON *.* TO 'u'",
		"REVOKE SELECT ON *.* FROM 'u'",
		"LOCK TABLES t WRITE",
		"SELECT * FROM t INTO OUTFILE '/tmp/x'",
		"SELECT * FROM t INTO DUMPFILE '/tmp/x'",
		"SELECT * FROM t FOR UPDATE",
		"/*x*/UPDATE t SET a=1",
		"WITH c AS (SELECT 1) DELETE FROM t",
		"WITH c AS (SELECT 1) UPDATE t SET a=1",
		"MERGE INTO t USING s",
	}
	for _, q := range denied {
		if err := CheckReadOnly(q); err == nil {
			t.Errorf("CheckReadOnly(%q) 应被拒绝", q)
		}
	}
}

func TestCheckReadOnly_CTE_select_allowed(t *testing.T) {
	// 只读 CTE 应放行。
	if err := CheckReadOnly("WITH c AS (SELECT 1) SELECT * FROM c"); err != nil {
		t.Errorf("只读 CTE 不应被拒绝: %v", err)
	}
}

func TestFirstWord(t *testing.T) {
	if FirstWord("  /*x*/   SELECT 1") != "select" {
		t.Fatal("FirstWord 未剥离注释")
	}
	if FirstWord("-- c\nUPDATE t") != "update" {
		t.Fatal("FirstWord 未剥离行注释")
	}
	_ = strings.TrimSpace
}
