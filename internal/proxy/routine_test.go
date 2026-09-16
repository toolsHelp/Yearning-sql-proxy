package proxy

import (
	"strings"
	"testing"
)

func TestParseRoutineName(t *testing.T) {
	cases := []struct {
		rest string
		db   string
		name string
	}{
		{"`gaia_supplier_product`.`nextval_val`", "gaia_supplier_product", "nextval_val"},
		{"gaia_supplier_product.nextval_val", "gaia_supplier_product", "nextval_val"},
		{"proc_advisory_source", "", "proc_advisory_source"},
		{"`addOneTenant`", "", "addOneTenant"},
		{"  `db`.`fn` ;", "db", "fn"},
		{"", "", ""},
	}
	for _, c := range cases {
		db, name := parseRoutineName(c.rest)
		if db != c.db || name != c.name {
			t.Errorf("parseRoutineName(%q) = (%q, %q), want (%q, %q)", c.rest, db, name, c.db, c.name)
		}
	}
}

func TestHandleRoutineCreateNotHandled(t *testing.T) {
	h := &Handler{}
	for _, q := range []string{
		"SHOW CREATE TABLE `db`.`t`",
		"SHOW CREATE VIEW v",
		"SELECT 1",
		"SHOW VARIABLES",
	} {
		if _, handled, _ := h.handleRoutineCreate(q, "show"); handled {
			t.Errorf("handleRoutineCreate(%q) 不应处理", q)
		}
	}
}

func TestHandleRoutineCreateEmptyForUnknownSchema(t *testing.T) {
	h := &Handler{} // schema 为空且名称未限定 → 返回空结果而非转发
	for _, q := range []string{
		"SHOW CREATE PROCEDURE proc_advisory_source",
		"SHOW CREATE FUNCTION nextval_val",
	} {
		r, handled, err := h.handleRoutineCreate(q, "show")
		if !handled || err != nil {
			t.Fatalf("handleRoutineCreate(%q) handled=%v err=%v", q, handled, err)
		}
		if r == nil || r.Resultset == nil || len(r.Values) != 0 {
			t.Errorf("handleRoutineCreate(%q) 应返回空结果集", q)
		}
	}
}

func TestBuildRoutineDDLProcedure(t *testing.T) {
	rows := []map[string]interface{}{
		{"definer": "gaia_dba@%", "sql_mode": "STRICT_TRANS_TABLES", "body": "begin\n  select 1;\nend",
			"cs_client": "utf8mb4", "coll_conn": "utf8mb4_general_ci", "db_collat": "utf8mb4_general_ci",
			"dtd": nil, "p_mode": "IN", "p_name": "p_tenant_id", "p_type": "bigint(20)"},
		{"p_mode": "OUT", "p_name": "o_ret", "p_type": "int(11)"},
	}
	got := routineRow(rows, "PROCEDURE", "db", "addOneTenant")[2]
	want := "CREATE DEFINER=`gaia_dba`@`%` PROCEDURE `db`.`addOneTenant`(IN `p_tenant_id` bigint(20), OUT `o_ret` int(11))\nbegin\n  select 1;\nend"
	if got != want {
		t.Errorf("buildRoutineDDL =\n%s\nwant\n%s", got, want)
	}
}

func TestBuildRoutineDDLFunction(t *testing.T) {
	rows := []map[string]interface{}{
		{"definer": "root@localhost", "sql_mode": "", "body": "return 1",
			"cs_client": "utf8", "coll_conn": "utf8_general_ci", "db_collat": "utf8_general_ci",
			"dtd": "int(11)", "p_mode": nil, "p_name": nil, "p_type": nil},
	}
	got := routineRow(rows, "FUNCTION", "db", "nextval")[2]
	want := "CREATE DEFINER=`root`@`localhost` FUNCTION `db`.`nextval`() RETURNS int(11)\nreturn 1"
	if got != want {
		t.Errorf("buildRoutineDDL =\n%s\nwant\n%s", got, want)
	}
}

func TestRoutineLookupQueryEscapesLiteral(t *testing.T) {
	q := routineLookupQuery("db'", "fn\\", "PROCEDURE")
	if !strings.Contains(q, "ROUTINE_SCHEMA = 'db'''") || !strings.Contains(q, "ROUTINE_NAME = 'fn\\\\'") {
		t.Errorf("routineLookupQuery 未转义字面量: %s", q)
	}
}

func TestRoutineRowEmptyParams(t *testing.T) {
	rows := []map[string]interface{}{
		{"definer": "u@h", "sql_mode": "x", "body": "null", "cs_client": "utf8mb4",
			"coll_conn": "utf8mb4_general_ci", "db_collat": "utf8mb4_general_ci", "dtd": nil},
	}
	row := routineRow(rows, "PROCEDURE", "db", "p")
	if row[2] != "CREATE DEFINER=`u`@`h` PROCEDURE `db`.`p`()\nnull" {
		t.Errorf("unexpected DDL: %v", row[2])
	}
}
