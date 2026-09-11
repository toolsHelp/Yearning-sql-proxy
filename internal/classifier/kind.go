package classifier

import "strings"

// Kind 标识一条 SQL 的语义类别，形如 "组.具体类别"，例如 "column.info_columns"。
//
// 分类只用于审计观测与后续路由重构的参考，不影响当前查询的处理行为。
// 无法归类的语句落 KindUnknown；报告里出现大量 unknown 说明分类规则需要补充。
type Kind string

// 各类别常量。命名规则：<组>.<类别>，组名与 Group() 的返回值一致。
const (
	// KindUnknown 未能识别的语句。
	KindUnknown Kind = "unknown"

	// 服务器特性 / 状态探测（Yearning 后端通常无权限，多被本地屏蔽）。
	KindProbeVariables   Kind = "probe.show_variables"
	KindProbeStatus      Kind = "probe.show_status"
	KindProbeGlobalVar   Kind = "probe.global_var"
	KindProbeEngines     Kind = "probe.engines"
	KindProbeProcesslist Kind = "probe.processlist"
	KindProbeCollation   Kind = "probe.collation"
	KindProbeCharset     Kind = "probe.charset"
	KindProbePlugins     Kind = "probe.plugins"
	KindProbeReplication Kind = "probe.replication"
	KindProbeOther       Kind = "probe.other"

	// 库级元数据。
	KindSchemaDatabases Kind = "schema.show_databases"
	KindSchemaSchemata  Kind = "schema.info_schemata"

	// 表级元数据。
	KindTableList      Kind = "table.show_tables"
	KindTableInfo      Kind = "table.info_tables"
	KindTableStatus    Kind = "table.show_table_status"
	KindTableCreate    Kind = "table.show_create_table"
	KindTableCreateOth Kind = "table.show_create_other"

	// 视图元数据。
	KindViewCreate Kind = "view.show_create_view"
	KindViewInfo   Kind = "view.info_views"

	// 列元数据。
	KindColumnShow Kind = "column.show_columns"
	KindColumnInfo Kind = "column.info_columns"
	// KindFieldList 来自 COM_FIELD_LIST 命令（不是 SQL），
	// Classify 不会产生该值，由代理在处理该命令时直接标记。
	KindFieldList Kind = "column.field_list"

	// 索引 / 约束元数据。
	KindIdxShow        Kind = "constraint.show_index"
	KindIdxStatistics  Kind = "constraint.info_statistics"
	KindIdxKeyUsage    Kind = "constraint.info_key_column_usage"
	KindIdxReferential Kind = "constraint.info_referential_constraints"
	KindIdxConstraints Kind = "constraint.info_table_constraints"

	// 存储过程 / 函数 / 触发器元数据。
	KindRoutineInfo     Kind = "routine.info_routines"
	KindRoutineParams   Kind = "routine.info_parameters"
	KindRoutineTriggers Kind = "routine.info_triggers"
	KindRoutineCreate   Kind = "routine.show_create_routine"
	KindRoutineStatus   Kind = "routine.show_status"

	// 权限 / 系统库探测。
	KindPrivGrants   Kind = "priv.show_grants"
	KindPrivInfo     Kind = "priv.info_privileges"
	KindPrivMySQLSys Kind = "priv.mysql_sys"

	// 会话控制。
	KindSessionUse      Kind = "session.use"
	KindSessionSet      Kind = "session.set"
	KindSessionTxn      Kind = "session.txn"
	KindSessionWarnings Kind = "session.show_warnings"

	// 业务查询。
	KindBizSelect  Kind = "biz.select"
	KindBizExplain Kind = "biz.explain"
	KindBizWith    Kind = "biz.with"
	KindBizOther   Kind = "biz.other"

	// 被只读校验拦截的写语句。
	KindWriteDDL Kind = "write.ddl"
	KindWriteDML Kind = "write.dml"

	// KindInfoOther 命中 information_schema 但未细分的结构表（events/partitions/files 等）。
	KindInfoOther Kind = "info.other"
	// KindShowOther 未细分的 SHOW 语句。
	KindShowOther Kind = "meta.show_other"
)

// Group 返回类别所属的组名（Kind 中第一个点号之前的部分）。
func (k Kind) Group() string {
	s := string(k)
	if i := strings.IndexByte(s, '.'); i >= 0 {
		return s[:i]
	}
	return s
}

// String 返回类别字符串，便于日志与报告直接输出。
func (k Kind) String() string { return string(k) }

// Classify 判定一条 SQL 的语义类别。
// firstWord 可由调用方传入 FirstWord(sql) 的结果；传空则由本函数自行计算。
func Classify(sql, firstWord string) Kind {
	if firstWord == "" {
		firstWord = FirstWord(sql)
	}
	switch firstWord {
	case "use":
		return KindSessionUse
	case "set":
		return KindSessionSet
	case "begin", "commit", "rollback", "savepoint", "release", "start":
		return KindSessionTxn
	case "show":
		return classifyShow(sql)
	case "select", "with", "table", "values":
		return classifyRead(sql, firstWord)
	case "explain", "desc", "describe":
		return KindBizExplain
	default:
		return classifyWrite(firstWord)
	}
}

// classifyShow 细分 SHOW 语句。
func classifyShow(sql string) Kind {
	s := normalizeForKind(sql)
	switch {
	case hasAny(s, "show databases", "show schemas"):
		return KindSchemaDatabases
	case hasAny(s, "show create table"):
		return KindTableCreate
	case hasAny(s, "show create view"):
		return KindViewCreate
	case hasAny(s, "show create procedure", "show create function", "show create trigger", "show create event"):
		return KindRoutineCreate
	case hasAny(s, "show create"):
		return KindTableCreateOth
	case hasAny(s, "show table status"):
		return KindTableStatus
	case hasAny(s, "show tables", "show open tables", "show full tables"):
		return KindTableList
	case hasAny(s, "show columns", "show fields", "show full columns", "show full fields"):
		return KindColumnShow
	case hasAny(s, "show index", "show indexes", "show keys"):
		return KindIdxShow
	case hasAny(s, "show triggers"):
		return KindRoutineTriggers
	case hasAny(s, "show function status", "show procedure status"):
		return KindRoutineStatus
	case hasAny(s, "show variables", "show session variables", "show global variables"):
		return KindProbeVariables
	case hasAny(s, "show status", "show session status", "show global status"):
		return KindProbeStatus
	case hasAny(s, "show engine"):
		return KindProbeEngines
	case hasAny(s, "show processlist"):
		return KindProbeProcesslist
	case hasAny(s, "show collation"):
		return KindProbeCollation
	case hasAny(s, "show character set", "show charset"):
		return KindProbeCharset
	case hasAny(s, "show plugins"):
		return KindProbePlugins
	case hasAny(s, "show grants", "show privileges"):
		return KindPrivGrants
	case hasAny(s, "show master", "show slave", "show binary", "show binlog"):
		return KindProbeReplication
	case hasAny(s, "show warnings", "show errors"):
		return KindSessionWarnings
	default:
		return KindShowOther
	}
}

// classifyRead 细分 SELECT / WITH / TABLE 等读语句。
func classifyRead(sql, firstWord string) Kind {
	s := normalizeForKind(sql)
	// 服务器变量/版本探测优先（SELECT @@version、SELECT VERSION()）。
	if strings.Contains(s, "@@") {
		return KindProbeGlobalVar
	}
	if hasAny(s, "version()", "database()", "user()", "schema()") && !hasAny(s, " from ") {
		return KindProbeGlobalVar
	}
	if k, ok := classifyInfoSchema(s); ok {
		return k
	}
	// mysql.* 系统库（mysql.user 等权限探测）。
	if hasAny(s, "from mysql.", "join mysql.") {
		return KindPrivMySQLSys
	}
	switch firstWord {
	case "with":
		return KindBizWith
	case "table", "values":
		return KindBizOther
	default:
		return KindBizSelect
	}
}

// classifyInfoSchema 细分 information_schema 查询。
// 匹配顺序按「更长的表名优先」，避免 tables 抢先命中 table_constraints。
func classifyInfoSchema(s string) (Kind, bool) {
	for _, m := range []struct {
		table string
		kind  Kind
	}{
		{"information_schema.table_constraints", KindIdxConstraints},
		{"information_schema.key_column_usage", KindIdxKeyUsage},
		{"information_schema.referential_constraints", KindIdxReferential},
		{"information_schema.statistics", KindIdxStatistics},
		{"information_schema.schemata", KindSchemaSchemata},
		{"information_schema.tables", KindTableInfo},
		{"information_schema.columns", KindColumnInfo},
		{"information_schema.views", KindViewInfo},
		{"information_schema.routines", KindRoutineInfo},
		{"information_schema.parameters", KindRoutineParams},
		{"information_schema.triggers", KindRoutineTriggers},
		{"information_schema.user_privileges", KindPrivInfo},
		{"information_schema.schema_privileges", KindPrivInfo},
		{"information_schema.table_privileges", KindPrivInfo},
		{"information_schema.column_privileges", KindPrivInfo},
		{"information_schema.global_privileges", KindPrivInfo},
		{"information_schema.collation_character_set_applicability", KindProbeCollation},
		{"information_schema.collations", KindProbeCollation},
		{"information_schema.character_sets", KindProbeCharset},
		{"information_schema.engines", KindProbeEngines},
		{"information_schema.plugins", KindProbePlugins},
		{"information_schema.profiling", KindProbeOther},
		{"information_schema.processlist", KindProbeProcesslist},
	} {
		if strings.Contains(s, m.table) {
			return m.kind, true
		}
	}
	if strings.Contains(s, "information_schema.") {
		return KindInfoOther, true
	}
	return KindUnknown, false
}

// classifyWrite 细分被只读校验拦截的写语句。
func classifyWrite(firstWord string) Kind {
	switch firstWord {
	case "create", "alter", "drop", "truncate", "rename":
		return KindWriteDDL
	case "insert", "update", "delete", "replace", "call", "grant", "revoke", "load":
		return KindWriteDML
	default:
		return KindBizOther
	}
}

// hasAny 判断 s 是否包含任一子串。
func hasAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// normalizeForKind 归一化 SQL 便于子串匹配：剥离注释、转小写、去反引号、压缩空白。
func normalizeForKind(sql string) string {
	s := stripComments(sql)
	s = strings.ReplaceAll(s, "`", "")
	s = strings.ToLower(s)
	return strings.Join(strings.Fields(s), " ")
}
