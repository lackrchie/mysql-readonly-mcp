// 安全校验器测试：正常用例 + 攻击用例
package guard

import (
	"strings"
	"testing"
)

var allowed = []string{"SELECT", "SHOW", "EXPLAIN", "DESCRIBE", "DESC", "WITH"}

func TestAllowedStatements(t *testing.T) {
	cases := []string{
		"SELECT * FROM users",
		"select id, name from customers where created_at > '2026-01-01' limit 10",
		"SELECT * FROM t;",                       // 末尾分号允许
		"  \n SELECT 1 ",                          // 首尾空白
		"SHOW TABLES",
		"SHOW CREATE TABLE users",
		"EXPLAIN SELECT * FROM orders",
		"DESCRIBE users",
		"DESC users",
		"WITH t AS (SELECT 1) SELECT * FROM t",   // CTE 查询
		"-- 注释\nSELECT 1",                       // 注释后是合法语句
		"SELECT ';' AS semi_in_string FROM dual", // 字符串里的分号：当前策略拒绝，见下面的用例
	}
	for _, sql := range cases[:len(cases)-1] {
		if _, err := Validate(sql, allowed); err != nil {
			t.Errorf("应该放行但被拒: %q -> %v", sql, err)
		}
	}
}

func TestRejectedStatements(t *testing.T) {
	cases := map[string]string{
		"UPDATE users SET name='x'":                          "写操作",
		"DELETE FROM users":                                  "写操作",
		"INSERT INTO users VALUES (1)":                       "写操作",
		"DROP TABLE users":                                   "DDL",
		"TRUNCATE TABLE users":                               "DDL",
		"ALTER TABLE users ADD COLUMN x INT":                 "DDL",
		"CREATE TABLE x (id INT)":                            "DDL",
		"GRANT ALL ON *.* TO 'a'@'%'":                        "权限操作",
		"SET GLOBAL max_connections=1":                       "SET",
		"SELECT 1; DROP TABLE users":                         "多语句注入",
		"SELECT 1;DELETE FROM users;":                        "多语句注入",
		"SELECT ';' FROM dual":                               "字符串内分号（宁严勿松）",
		"-- 注释\nDROP TABLE users":                           "注释伪装",
		"/* c */ DELETE FROM users":                          "注释伪装",
		"/*! DELETE FROM users */":                           "版本注释会被 MySQL 执行",
		"SELECT * FROM users INTO OUTFILE '/tmp/x'":          "写服务器文件",
		"SELECT * INTO DUMPFILE '/tmp/x' FROM users LIMIT 1": "写服务器文件",
		"SELECT * FROM users FOR UPDATE":                     "加锁",
		"SELECT * FROM users LOCK IN SHARE MODE":             "加锁",
		"SELECT SLEEP(100)":                                  "拖库",
		"SELECT BENCHMARK(100000000, MD5('x'))":              "拖库",
		"SELECT GET_LOCK('a', 100)":                          "加锁",
		"SELECT LOAD_FILE('/etc/passwd')":                    "读服务器文件",
		"WITH t AS (SELECT 1) DELETE FROM users":             "CTE 接写语句",
		"WITH t AS (SELECT 1) UPDATE users SET a=1":          "CTE 接写语句",
		"":                                                    "空语句",
		"/* 未闭合":                                            "未闭合注释",
		"CALL some_proc()":                                   "存储过程可能有写操作",
	}
	for sql, why := range cases {
		if _, err := Validate(sql, allowed); err == nil {
			t.Errorf("应该被拒但放行了（%s）: %q", why, sql)
		}
	}
}

func TestWithSubqueryContainingKeywordInString(t *testing.T) {
	// 字符串里的 UPDATE 字样不应误伤
	sql := "WITH t AS (SELECT 'UPDATE is a word' AS s) SELECT * FROM t"
	if _, err := Validate(sql, allowed); err != nil {
		t.Errorf("字符串中的关键字被误伤: %v", err)
	}
	// 子查询（深度>0）里的关键字字样也不该触发（虽然子查询里也不可能有合法 DELETE）
	sql2 := "WITH t AS (SELECT 1) SELECT * FROM t WHERE s != 'DELETE'"
	if _, err := Validate(sql2, allowed); err != nil {
		t.Errorf("误伤: %v", err)
	}
}

func TestCleanedOutput(t *testing.T) {
	out, err := Validate("  SELECT 1;  ", allowed)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, ";") {
		t.Errorf("清理后不应含分号: %q", out)
	}
}
