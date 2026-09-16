package proxy

import (
	"fmt"
	"log"
	"strings"

	"github.com/go-mysql-org/go-mysql/mysql"
)

// 本文件拦截 SHOW CREATE PROCEDURE / SHOW CREATE FUNCTION，改为查询
// information_schema 后在本地拼装 SHOW CREATE 形状的结果集。
//
// 不能直接转发：Yearning 后端的 TiDB 解析器不支持 SHOW CREATE FUNCTION，
// 直接报 `line 1 column 20 near "function ..."`；SHOW CREATE PROCEDURE 未限定
// 库名时会按当前 schema 路由到错误的数据源，真实 MySQL 报 1305（过程不存在）。
// routines.ROUTINE_DEFINITION 只有函数体，DDL 头部（参数/返回类型）由
// information_schema.parameters 拼出，与真实 SHOW CREATE 输出可能有细微差别。

// routineInfo 是改写后的 routines 查询单行里取例行字段用的键。
type routineInfo struct {
	definer  string
	sqlMode  string
	body     string
	csClient string
	collConn string
	dbCollat string
	dtd      string
	params   []routineParam
}

// routineParam 是 parameters 表里的一行（不含返回类型行）。
type routineParam struct {
	mode string
	name string
	typ  string
}

// handleRoutineCreate 拦截 SHOW CREATE PROCEDURE/FUNCTION 并本地拼装结果。
// 返回 (nil, false, nil) 表示不处理，调用方继续走原有链路。
func (h *Handler) handleRoutineCreate(q, firstWord string) (*mysql.Result, bool, error) {
	if firstWord != "show" {
		return nil, false, nil
	}
	lower := strings.ToLower(q)
	kind, colObj, colDDL := "", "", ""
	for _, k := range []struct {
		keyword, obj, ddl string
	}{
		{"show create procedure", "Procedure", "Create Procedure"},
		{"show create function", "Function", "Create Function"},
	} {
		if i := strings.Index(lower, k.keyword); i >= 0 {
			kind = strings.ToUpper(k.keyword[len("show create "):])
			colObj, colDDL = k.obj, k.ddl
			q = q[i+len(k.keyword):]
			break
		}
	}
	if kind == "" {
		return nil, false, nil
	}

	db, name := parseRoutineName(q)
	if name == "" {
		return nil, false, nil
	}
	if db == "" {
		h.mu.Lock()
		db = h.schema
		h.mu.Unlock()
	}
	cols := []string{colObj, "sql_mode", colDDL, "character_set_client", "collation_connection", "Database Collation"}
	if !isRealSchema(db) {
		// 库名未知或系统库：不转发，返回空结果避免 DataGrip 报错。
		return emptyResultWithCols(cols), true, nil
	}

	log.Printf("本地改写 SHOW CREATE %s: %s.%s", kind, db, name)
	rq := routineLookupQuery(db, name, kind)
	targets, err := h.targetSources(db, rq)
	if err != nil {
		return nil, true, myError(1044, err.Error())
	}
	raw, _, err := h.execAcross(rq, db, targets)
	if err != nil {
		return nil, true, queryError(err)
	}
	if len(raw.Results) == 0 || len(raw.Results[0].Data) == 0 {
		return emptyResultWithCols(cols), true, nil
	}
	row := routineRow(raw.Results[0].Data, kind, db, name)
	rs, err := mysql.BuildSimpleTextResultset(cols, [][]interface{}{row})
	if err != nil {
		return emptyResultWithCols(cols), true, nil
	}
	setUTF8Charset(rs)
	return &mysql.Result{Status: 0x0002, Resultset: rs}, true, nil
}

// routineLookupQuery 生成改写后的查询：routines 行 LEFT JOIN parameters 行，
// 参数行按 ORDINAL_POSITION 排在后面（0 号为函数返回类型，PARAMETER_NAME 为 NULL）。
func routineLookupQuery(db, name, kind string) string {
	esc := strings.NewReplacer("'", "''", "\\", "\\\\")
	return fmt.Sprintf(`SELECT r.DEFINER AS definer, r.SQL_MODE AS sql_mode, `+
		`r.ROUTINE_DEFINITION AS body, r.CHARACTER_SET_CLIENT AS cs_client, `+
		`r.COLLATION_CONNECTION AS coll_conn, r.DATABASE_COLLATION AS db_collat, `+
		`r.DTD_IDENTIFIER AS dtd, p.PARAMETER_MODE AS p_mode, p.PARAMETER_NAME AS p_name, `+
		`p.DTD_IDENTIFIER AS p_type FROM information_schema.routines r `+
		`LEFT JOIN information_schema.parameters p ON p.SPECIFIC_SCHEMA = r.ROUTINE_SCHEMA `+
		`AND p.SPECIFIC_NAME = r.ROUTINE_NAME WHERE r.ROUTINE_SCHEMA = '%s' `+
		`AND r.ROUTINE_NAME = '%s' AND r.ROUTINE_TYPE = '%s' ORDER BY p.ORDINAL_POSITION`,
		esc.Replace(db), esc.Replace(name), kind)
}

// routineRow 从改写查询的结果行拼出 SHOW CREATE 的一行输出。
func routineRow(rows []map[string]interface{}, kind, db, name string) []interface{} {
	var ri routineInfo
	get := func(row map[string]interface{}, key string) string {
		if v, ok := row[key]; ok && v != nil {
			return fmt.Sprintf("%v", v)
		}
		return ""
	}
	for i, r := range rows {
		if i == 0 {
			ri = routineInfo{
				definer:  get(r, "definer"),
				sqlMode:  get(r, "sql_mode"),
				body:     get(r, "body"),
				csClient: get(r, "cs_client"),
				collConn: get(r, "coll_conn"),
				dbCollat: get(r, "db_collat"),
				dtd:      get(r, "dtd"),
			}
		}
		if pn := get(r, "p_name"); pn != "" {
			ri.params = append(ri.params, routineParam{mode: get(r, "p_mode"), name: pn, typ: get(r, "p_type")})
		}
	}
	ddl := buildRoutineDDL(ri, kind, db, name)
	return []interface{}{
		"`" + db + "`.`" + name + "`",
		ri.sqlMode,
		ddl,
		ri.csClient,
		ri.collConn,
		ri.dbCollat,
	}
}

// buildRoutineDDL 拼 CREATE PROCEDURE/FUNCTION 语句。
func buildRoutineDDL(ri routineInfo, kind, db, name string) string {
	definer := ""
	if i := strings.Index(ri.definer, "@"); i >= 0 {
		definer = fmt.Sprintf("DEFINER=`%s`@`%s` ", ri.definer[:i], ri.definer[i+1:])
	}
	parts := make([]string, 0, len(ri.params))
	for _, p := range ri.params {
		s := ""
		if p.mode != "" {
			s = strings.ToUpper(p.mode) + " "
		}
		parts = append(parts, s+"`"+p.name+"` "+p.typ)
	}
	head := fmt.Sprintf("CREATE %s%s `%s`.`%s`(", definer, kind, db, name)
	if len(parts) > 0 {
		head += strings.Join(parts, ", ")
	}
	head += ")"
	if kind == "FUNCTION" && ri.dtd != "" {
		head += " RETURNS " + ri.dtd
	}
	return head + "\n" + ri.body
}

// parseRoutineName 解析 SHOW CREATE 后的对象名，返回 (库名, 对象名)。
// 支持 `db`.`name` / db.name / `name` / name。
func parseRoutineName(s string) (db, name string) {
	s = strings.TrimSpace(s)
	if db = readQualifier(s); db != "" {
		if strings.HasPrefix(s, "`") {
			end := strings.IndexByte(s[1:], '`')
			s = strings.TrimSpace(s[end+2:])
		} else {
			s = strings.TrimSpace(s[len(db):])
		}
		s = strings.TrimSpace(strings.TrimPrefix(s, "."))
		return db, strings.Trim(readIdent(s), "`")
	}
	return "", strings.Trim(readQuoted(s), "`")
}

// emptyResultWithCols 返回指定列名的空结果集（列解析失败时退化为单列占位）。
func emptyResultWithCols(cols []string) *mysql.Result {
	rs, err := mysql.BuildSimpleTextResultset(cols, nil)
	if err != nil {
		return emptyResult()
	}
	setUTF8Charset(rs)
	return &mysql.Result{Status: 0x0002, Resultset: rs}
}
