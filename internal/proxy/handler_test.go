package proxy

import (
	"testing"

	"github.com/toolsHelp/Yearning-sql-proxy/internal/classifier"
)

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
		// SHOW ... FROM 表 FROM 库：库名在最后一个 FROM 后，不能把表名当库名
		{"SHOW FULL COLUMNS FROM `b_attachment` FROM `gaia_poseidon_supplier` LIKE '%'", "gaia_poseidon_supplier"},
		{"SHOW INDEX FROM `t_service_provider` FROM `gaia_srm_service`", "gaia_srm_service"},
		{"SHOW KEYS FROM t FROM `gaia_edi`", "gaia_edi"},
		// 只有一个 FROM 且无库限定的列/索引查询：标识符是表名，不覆盖当前 schema
		{"SHOW COLUMNS FROM t", ""},
		// SHOW CREATE TABLE / VIEW 的库名路由
		{"SHOW CREATE TABLE `gaia_edi`.`t_order`", "gaia_edi"},
		{"show create table gaia_edi.t_order", "gaia_edi"},
		{"SHOW CREATE VIEW `gaia_edi`.`v_order`", "gaia_edi"},
		{"SHOW CREATE TABLE information_schema.COLUMNS", ""},
		// 无库名
		{"SELECT 1", ""},
		{"SELECT @@session.auto_increment", ""},
		// DataGrip 的 information_schema 内省写法（此前解析不出库名）
		{"select 1 from information_schema.tables where lower(table_schema) = 'order_db' " +
			"and lower(table_name) = 't'", "order_db"},
		{"select T.table_name as table_name from information_schema.tables T " +
			"where T.table_schema = 'order_db'", "order_db"},
		{"select table_name from information_schema.tables where t.table_schema='order_db'", "order_db"},
		{"select table_name from information_schema.tables where table_schema in ('order_db')", "order_db"},
		{"select ordinal_position, column_name from information_schema.columns " +
			"where table_schema = 'gaia_edi'", "gaia_edi"},
		// 别名 select table_schema as schema_name 不应被当成库名过滤条件
		{"select table_schema as schema_name, table_name as major_name " +
			"from information_schema.tables where table_schema = 'order_db'", "order_db"},
		// routine/trigger/event/parameters 用各自的 *_schema 列过滤
		{"select trigger_name from information_schema.triggers " +
			"where trigger_schema = 'gaia_poseidon_supplier' and true", "gaia_poseidon_supplier"},
		{"select event_name from information_schema.events " +
			"where event_schema = 'gaia_poseidon_supplier' and true", "gaia_poseidon_supplier"},
		{"select specific_name from information_schema.parameters " +
			"where specific_schema = 'gaia_poseidon_supplier' and ordinal_position > 0", "gaia_poseidon_supplier"},
		{"select routine_name from information_schema.routines " +
			"where routine_schema = 'gaia_poseidon_supplier'", "gaia_poseidon_supplier"},
		// 全量内省查询（无库名）应返回空，交给聚合模式处理
		{"select table_schema as schema_name, table_name as major_name " +
			"from information_schema.tables", ""},
		// JOIN 条件里的列标识符（T.table_schema = V.table_schema）不能被当成库名，
		// 否则 DataGrip 的「Retrieve Tables and Views」会去查一个叫 V 的库、返回 0 行。
		{"select T.table_name as table_name, T.table_type as table_type " +
			"from information_schema.tables T left join information_schema.views V " +
			"on T.table_schema = V.table_schema and T.table_name = V.table_name " +
			"where T.table_schema = 'gaia_poseidon_supplier' and true", "gaia_poseidon_supplier"},
		{"select T.table_schema as schema_name, T.table_name as major_name, " +
			"C.ordinal_position as position from information_schema.tables T, " +
			"information_schema.columns C where T.table_schema in ( 'gaia_edi' ) " +
			"and T.table_schema = C.table_schema and T.table_name = C.table_name", "gaia_edi"},
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

func TestHandleServerProbeQueryStatus(t *testing.T) {
	cases := []struct {
		q    string
		want bool
	}{
		{"SHOW STATUS", true},
		{"SHOW SESSION STATUS", true},
		{"SHOW GLOBAL STATUS", true},
		{"SHOW VARIABLES", true},
		{"SHOW VARIABLES LIKE 'character_set_%'", true},
		{"SHOW SESSION VARIABLES", true},
		// 函数/存储过程状态不能被 " status" 误伤，否则 DataGrip 的对应节点永远为空
		{"SHOW FUNCTION STATUS WHERE Db = 'gaia_edi'", false},
		{"SHOW PROCEDURE STATUS WHERE Db = 'gaia_edi' AND Name LIKE '%'", false},
	}
	for _, c := range cases {
		if got := handleServerProbeQuery(c.q, classifier.FirstWord(c.q)); got != c.want {
			t.Errorf("handleServerProbeQuery(%q) = %v, want %v", c.q, got, c.want)
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
		// DataGrip 走 SHOW 语法的结构查询（伪装 5.7 时不会发 information_schema）
		"show full tables from `gaia_edi` like '%'",
		"show full columns from `b_attachment` from `gaia_edi` like '%'",
		"show index from `t` from `gaia_edi`",
		"show keys from `t` from `gaia_edi`",
		"show create table `gaia_edi`.`t`",
		"show table status from `gaia_edi`",
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
		// 非结构类语句不应缓存
		"show variables",
		"show databases",
		"show processlist",
		"show function status where db = 'gaia_edi'",
		"select * from gaia_edi.t",
	}
	for _, q := range notCacheable {
		if isCacheableMetaQuery(q) {
			t.Errorf("不应缓存: %s", q)
		}
	}
}
