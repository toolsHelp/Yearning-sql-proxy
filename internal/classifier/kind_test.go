package classifier

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		q    string
		want Kind
	}{
		// 会话控制
		{"USE `gaia_edi`", KindSessionUse},
		{"SET NAMES utf8mb4", KindSessionSet},
		{"SET @@session.sql_mode = ''", KindSessionSet},
		{"BEGIN", KindSessionTxn},
		{"COMMIT", KindSessionTxn},

		// 库级
		{"show databases", KindSchemaDatabases},
		{"SHOW DATABASES;", KindSchemaDatabases},
		{"SELECT SCHEMA_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='my_db'", KindSchemaSchemata},

		// 表级
		{"SHOW TABLES FROM gaia_purchase_order", KindTableList},
		{"SHOW FULL TABLES FROM `gaia_edi`", KindTableList},
		{"SHOW TABLE STATUS FROM `gaia_edi`", KindTableStatus},
		{"select T.table_name from information_schema.tables T where T.table_schema = 'gaia_edi'", KindTableInfo},
		{"SHOW CREATE TABLE `gaia_edi`.`t_order`", KindTableCreate},
		{"SHOW CREATE VIEW `gaia_edi`.`v_order`", KindViewCreate},
		{"SHOW CREATE PROCEDURE p1", KindRoutineCreate},

		// 列
		{"SHOW COLUMNS FROM `gaia_edi`.`t`", KindColumnShow},
		{"SHOW FULL FIELDS FROM t", KindColumnShow},
		{"select ordinal_position from information_schema.columns where table_schema = 'x'", KindColumnInfo},
		{"select ordinal_position from `information_schema`.`columns` where table_schema = 'x'", KindColumnInfo},

		// 索引 / 约束
		{"SHOW INDEX FROM t FROM db1", KindIdxShow},
		{"select table_name, index_name from information_schema.statistics where table_schema = 'x'", KindIdxStatistics},
		{"select * from information_schema.key_column_usage where table_schema = 'x'", KindIdxKeyUsage},
		{"select * from information_schema.table_constraints where table_schema = 'x'", KindIdxConstraints},
		{"select * from information_schema.referential_constraints", KindIdxReferential},

		// 视图 / 过程
		{"select table_name, view_definition from information_schema.views where table_schema = 'x'", KindViewInfo},
		{"select * from information_schema.routines", KindRoutineInfo},
		{"select * from information_schema.parameters", KindRoutineParams},
		{"select * from information_schema.triggers", KindRoutineTriggers},
		{"SHOW TRIGGERS FROM db1", KindRoutineTriggers},
		{"SHOW FUNCTION STATUS WHERE Db = 'db1'", KindRoutineStatus},
		{"SHOW PROCEDURE STATUS WHERE Db = 'db1' AND Name LIKE '%'", KindRoutineStatus},
		{"SHOW STATUS", KindProbeStatus},
		{"SHOW SESSION STATUS", KindProbeStatus},

		// 服务器探测
		{"SELECT @@session.auto_increment", KindProbeGlobalVar},
		{"SELECT @@version, @@version_comment", KindProbeGlobalVar},
		{"SELECT VERSION()", KindProbeGlobalVar},
		{"SELECT DATABASE()", KindProbeGlobalVar},
		{"SHOW VARIABLES", KindProbeVariables},
		{"SHOW SESSION STATUS", KindProbeStatus},
		{"SHOW ENGINES", KindProbeEngines},
		{"SHOW PROCESSLIST", KindProbeProcesslist},
		{"SHOW COLLATION", KindProbeCollation},
		{"select * from information_schema.collations", KindProbeCollation},
		{"select * from information_schema.character_sets", KindProbeCharset},
		{"select * from information_schema.plugins", KindProbePlugins},
		{"SHOW MASTER STATUS", KindProbeReplication},

		// 权限
		{"SHOW GRANTS", KindPrivGrants},
		{"select grantee from information_schema.user_privileges", KindPrivInfo},
		{"select * from information_schema.schema_privileges", KindPrivInfo},
		{"select user from mysql.user", KindPrivMySQLSys},

		// 业务查询
		{"SELECT t.* FROM gaia_edi.some_table t LIMIT 1", KindBizSelect},
		{"SELECT * FROM `gaia_srm_common`.t", KindBizSelect},
		{"SELECT 1", KindBizSelect},
		{"EXPLAIN SELECT * FROM t", KindBizExplain},
		{"DESC t", KindBizExplain},
		{"WITH x AS (SELECT 1) SELECT * FROM x", KindBizWith},
		{"select * from information_schema.events", KindInfoOther},

		// 写语句（会被只读校验拦截）
		{"UPDATE t SET a = 1", KindWriteDML},
		{"DELETE FROM t", KindWriteDML},
		{"DROP TABLE t", KindWriteDDL},
		{"ALTER TABLE t ADD COLUMN c INT", KindWriteDDL},

		// 带 DataGrip 注释头前缀
		{"/* ApplicationName=DataGrip */ SELECT * FROM t", KindBizSelect},
		{"/* ApplicationName=DataGrip */ show databases", KindSchemaDatabases},
	}
	for _, c := range cases {
		if got := Classify(c.q, FirstWord(c.q)); got != c.want {
			t.Errorf("Classify(%q) = %q, want %q", c.q, got, c.want)
		}
	}
}

func TestClassifyEmptyFirstWord(t *testing.T) {
	// firstWord 传空时应自行计算。
	if got := Classify("show databases", ""); got != KindSchemaDatabases {
		t.Errorf("Classify 未自行计算 firstWord: got %q", got)
	}
}

func TestKindGroup(t *testing.T) {
	cases := []struct {
		k    Kind
		want string
	}{
		{KindColumnInfo, "column"},
		{KindTableInfo, "table"},
		{KindUnknown, "unknown"},
	}
	for _, c := range cases {
		if got := c.k.Group(); got != c.want {
			t.Errorf("Kind(%q).Group() = %q, want %q", c.k, got, c.want)
		}
	}
}
