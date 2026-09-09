// 连接诊断：独立探测服务端能力，帮助定位连接/兼容性问题
package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// Diagnose 对给定 DSN 做一系列探测，返回人类可读的诊断报告（不打印密码）。
func Diagnose(dsn string, timeoutSeconds int) string {
	var b strings.Builder
	line := func(ok bool, name, detail string) {
		mark := "✔"
		if !ok {
			mark = "✘"
		}
		fmt.Fprintf(&b, "  %s %s%s\n", mark, name, detail)
	}

	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return fmt.Sprintf("DSN 解析失败：%v\n（检查 config.yaml 的 dsn 格式）", err)
	}
	// 隐藏密码后回显连接目标，便于确认连的是哪台
	fmt.Fprintf(&b, "连接目标：%s@%s/%s\n", cfg.User, cfg.Addr, cfg.DBName)
	fmt.Fprintln(&b, "探测结果：")

	cfg.MultiStatements = false
	cfg.ParseTime = true
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return b.String() + fmt.Sprintf("  ✘ 打开驱动失败：%v\n", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(2)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	// 1. 基础连通 + 认证
	if err := db.PingContext(ctx); err != nil {
		line(false, "连接/认证", "："+err.Error())
		b.WriteString("\n→ 连不上或密码错。若是 Access denied，说明解出的密码不正确；\n" +
			"  若是网络超时，检查主机/端口与网络可达性。\n")
		return b.String()
	}
	line(true, "连接/认证", "（主机可达、账号密码正确）")

	// 2. 版本
	var version string
	if err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err == nil {
		line(true, "SELECT 查询", "：服务端版本 "+version)
	} else {
		line(false, "SELECT 查询", "："+err.Error())
	}

	// 3. 当前库
	var curDB sql.NullString
	if err := db.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&curDB); err == nil {
		if curDB.Valid && curDB.String != "" {
			line(true, "当前数据库", "："+curDB.String)
		} else {
			line(false, "当前数据库", "：未选择（DSN 的 / 后没写库名，list_tables 将为空）")
		}
	}

	// 4. 只读事务支持（本服务的只读兜底机制）
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		line(false, "只读事务", "："+err.Error()+"（将降级为普通查询，只读由应用层白名单保证）")
	} else {
		var one int
		err = tx.QueryRowContext(ctx, "SELECT 1").Scan(&one)
		tx.Rollback()
		if err == nil {
			line(true, "只读事务", "（服务端只读兜底可用）")
		} else {
			line(false, "只读事务", "："+err.Error())
		}
	}

	// 5. 可见数据库
	if rows, err := db.QueryContext(ctx, "SHOW DATABASES"); err == nil {
		var dbs []string
		for rows.Next() {
			var name string
			if rows.Scan(&name) == nil {
				dbs = append(dbs, name)
			}
		}
		rows.Close()
		line(true, "可见数据库", "："+strings.Join(dbs, ", "))
	}

	b.WriteString("\n→ 若以上关键项均为 ✔，则 MCP 服务可正常工作。\n")
	if !curDB.Valid || curDB.String == "" {
		b.WriteString("→ 提示：DSN 里没写库名。从上面「可见数据库」挑一个 CRM 库，\n" +
			"  填到 config.yaml 的 dsn 中 tcp(...)/ 之后，再用 list_tables 查表。\n")
	}
	return b.String()
}
