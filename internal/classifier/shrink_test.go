package classifier

import "testing"

func TestShrinkSQL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// 引号外空白压缩
		{"SELECT  *\n  FROM  t", "SELECT * FROM t"},
		{"SELECT\t*\tFROM t", "SELECT * FROM t"},
		{"SELECT 1", "SELECT 1"},
		// 字面量内容原样保留
		{"SELECT 'a  b'  FROM t", "SELECT 'a  b' FROM t"},
		{"SELECT '张 三' FROM t", "SELECT '张 三' FROM t"},
		{`SELECT "x\ny" FROM t`, `SELECT "x\ny" FROM t`},
		{"SELECT `col a` FROM t", "SELECT `col a` FROM t"},
		// 注释去除
		{"SELECT 1 -- c\nFROM t", "SELECT 1 FROM t"},
		{"SELECT /* c1 */ 1 /*c2*/", "SELECT 1"},
		{"SELECT 1 # tail", "SELECT 1"},
		{"-- leading\nSELECT 1", "SELECT 1"},
		// 注释里的引号不影响状态机
		{"SELECT 1 -- don't\nFROM t", "SELECT 1 FROM t"},
		// 减号不是注释
		{"SELECT a--b", "SELECT a--b"},
		// 转义引号
		{`SELECT 'it\'s' FROM t`, `SELECT 'it\'s' FROM t`},
	}
	for _, c := range cases {
		if got := ShrinkSQL(c.in); got != c.want {
			t.Errorf("ShrinkSQL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestShrinkSQLShortens(t *testing.T) {
	// 模拟 DataGrip 格式化的长 SQL：缩进 + 换行占比高
	long := "-- Retrieve Something\nselect column_name,\n       column_type,\n       column_comment\nfrom information_schema.columns\nwhere table_schema = 'gaia_edi'\n  and true\norder by table_name, ordinal_position"
	got := ShrinkSQL(long)
	if got != "select column_name, column_type, column_comment from information_schema.columns where table_schema = 'gaia_edi' and true order by table_name, ordinal_position" {
		t.Errorf("压缩结果不符合预期: %q", got)
	}
	if len(got) >= len(long) {
		t.Errorf("压缩后应更短: %d -> %d", len(long), len(got))
	}
}
