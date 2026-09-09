package proxy

import "testing"

func TestExtractTargetSchema(t *testing.T) {
	cases := []struct {
		q    string
		want string
	}{
		{"SELECT t.* FROM gaia_edi.some_table t LIMIT 1", "gaia_edi"},
		{"SELECT * FROM `gaia_srm_common`.t", "gaia_srm_common"},
		{"show databases", ""},
		// WHERE TABLE_SCHEMA = 'x'
		{"SELECT table_name FROM information_schema.TABLES T WHERE T.TABLE_SCHEMA = 'gaia_edi'", "gaia_edi"},
		{"SELECT SCHEMA_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='my_db'", "my_db"},
		// 系统库应排除
		{"SELECT table_name FROM information_schema.TABLES WHERE TABLE_SCHEMA = 'information_schema'", ""},
		// SHOW TABLES FROM x
		{"SHOW TABLES FROM gaia_purchase_order", "gaia_purchase_order"},
		{"SHOW FULL TABLES FROM `gaia_edi`", "gaia_edi"},
		{"SHOW COLUMNS FROM `gaia_edi`.`t`", "gaia_edi"},
		// SHOW CREATE TABLE / VIEW 的库名路由
		{"SHOW CREATE TABLE `gaia_edi`.`t_order`", "gaia_edi"},
		{"show create table gaia_edi.t_order", "gaia_edi"},
		{"SHOW CREATE VIEW `gaia_edi`.`v_order`", "gaia_edi"},
		{"SHOW CREATE TABLE information_schema.COLUMNS", ""},
		// 无库名
		{"SELECT 1", ""},
		{"SELECT @@session.auto_increment", ""},
	}
	for _, c := range cases {
		got := extractTargetSchema(c.q)
		if got != c.want {
			t.Errorf("extractTargetSchema(%q) = %q, want %q", c.q, got, c.want)
		}
	}
}

func TestReadQuoted(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"'gaia_edi'", "gaia_edi"},
		{"`gaia_edi`", "gaia_edi"},
		{`"gaia_edi"`, "gaia_edi"},
		{"gaia_edi", "gaia_edi"},
	}
	for _, c := range cases {
		if got := readQuoted(c.in); got != c.want {
			t.Errorf("readQuoted(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeSQL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"SELECT 1", "SELECT 1"},
		{"/* ApplicationName=DataGrip */ SELECT 1 ; ", "SELECT 1"},
		{"select   *    from   t", "select * from t"},
		{"-- comment\nSELECT 2", "SELECT 2"},
		{"SELECT a /* inline */ FROM b", "SELECT a FROM b"},
	}
	for _, c := range cases {
		if got := normalizeSQL(c.in); got != c.want {
			t.Errorf("normalizeSQL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsCacheableMetaQuery(t *testing.T) {
	cacheable := []string{
		"select T.table_name from information_schema.tables T where T.table_schema = 'gaia_edi'",
		"select ordinal_position from information_schema.columns where table_schema = 'x'",
		"select table_name, index_name from information_schema.statistics where table_schema = 'x'",
		"select table_name, view_definition from information_schema.views where table_schema = 'x'",
	}
	for _, q := range cacheable {
		if !isCacheableMetaQuery(q) {
			t.Errorf("应可缓存: %s", q)
		}
	}
	notCacheable := []string{
		"SELECT * FROM gaia_edi.some_table",
		"select grantee from information_schema.user_privileges",
		"select ... from information_schema.tables union all select ... from information_schema.routines",
	}
	for _, q := range notCacheable {
		if isCacheableMetaQuery(q) {
			t.Errorf("不应缓存: %s", q)
		}
	}
}
