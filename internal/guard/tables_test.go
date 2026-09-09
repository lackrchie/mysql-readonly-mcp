// 表白名单校验测试
package guard

import "testing"

var whitelist = []string{"customers", "orders"}

func TestTablesAllowed(t *testing.T) {
	cases := []string{
		"SELECT * FROM customers",
		"SELECT * FROM Customers",                     // 大小写不敏感
		"SELECT * FROM `customers`",                   // 反引号
		"SELECT * FROM crm.customers",                 // schema 前缀
		"SELECT * FROM customers c JOIN orders o ON o.customer_id = c.id",
		"SELECT * FROM customers, orders",             // 逗号多表
		"SELECT * FROM customers c, orders o WHERE c.id = o.customer_id",
		"SELECT * FROM (SELECT * FROM orders) t",      // 派生表
		"SELECT c.name, (SELECT COUNT(*) FROM orders o WHERE o.customer_id = c.id) FROM customers c",
		"WITH top AS (SELECT * FROM orders) SELECT * FROM top JOIN customers ON 1=1", // CTE 名可引用
		"SELECT COUNT(*) FROM customers WHERE name = 'FROM users'",                   // 字符串里的 FROM 不误伤
		"SELECT * FROM orders LEFT JOIN customers ON orders.customer_id = customers.id",
		"SELECT 1",                                     // 无表查询
	}
	for _, sql := range cases {
		if err := CheckTables(sql, whitelist); err != nil {
			t.Errorf("应该放行但被拒: %q -> %v", sql, err)
		}
	}
}

func TestTablesRejected(t *testing.T) {
	cases := []string{
		"SELECT * FROM users",
		"SELECT * FROM customers JOIN payments ON 1=1",             // JOIN 白名单外的表
		"SELECT * FROM customers, secret_table",                    // 逗号多表混入
		"SELECT * FROM (SELECT * FROM admin_users) t",              // 派生表内引用
		"SELECT c.name, (SELECT 1 FROM salaries s) FROM customers c", // 子查询引用
		"WITH t AS (SELECT * FROM employees) SELECT * FROM t",      // CTE 体内引用白名单外的表
		"SELECT * FROM information_schema.tables",                  // information_schema 也要拦
		"SELECT * FROM crm.users",                                  // schema 前缀不影响判断
	}
	for _, sql := range cases {
		if err := CheckTables(sql, whitelist); err == nil {
			t.Errorf("应该被拒但放行了: %q", sql)
		}
	}
}

func TestEmptyWhitelistMeansNoLimit(t *testing.T) {
	if err := CheckTables("SELECT * FROM anything", nil); err != nil {
		t.Errorf("空白名单应不限制: %v", err)
	}
}
