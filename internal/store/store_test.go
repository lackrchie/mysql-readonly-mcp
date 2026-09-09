// 真实连接测试：需要 db_mcp 根目录存在 config.yaml 才会执行，
// 否则自动跳过（不影响 go test ./... 在无数据库环境下通过）。
//
// 运行方式：
//   cd db_mcp && go test ./internal/store/ -run TestRealConnection -v
package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"db-mcp/internal/config"
)

// loadConfigOrSkip 定位项目根目录的 config.yaml；不存在则跳过测试
func loadConfigOrSkip(t *testing.T) *config.Config {
	t.Helper()
	// 从 internal/store 往上两级到项目根
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(root, "config.yaml")
	if _, err := os.Stat(cfgPath); err != nil {
		t.Skipf("未找到 %s，跳过真实连接测试（先 cp config.example.yaml config.yaml 并填写）", cfgPath)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("加载配置失败：%v", err)
	}
	return cfg
}

// TestRealConnection 打印一份连接诊断报告（连通/认证/版本/只读事务/可见库）
func TestRealConnection(t *testing.T) {
	cfg := loadConfigOrSkip(t)
	report := Diagnose(cfg.Database.DSN, cfg.Query.TimeoutSeconds)
	t.Log("\n" + report)
}

// TestSelectOne 最小可用性验证：连上并执行 SELECT 1
func TestSelectOne(t *testing.T) {
	cfg := loadConfigOrSkip(t)
	st, err := Open(cfg.Database.DSN, cfg.Query.TimeoutSeconds, cfg.Query.MaxRows)
	if err != nil {
		t.Fatalf("连接失败：%v", err)
	}
	res, err := st.Query(context.Background(), "SELECT 1 AS ok")
	if err != nil {
		t.Fatalf("查询失败：%v", err)
	}
	if res.RowCount != 1 {
		t.Fatalf("期望 1 行，得到 %d 行", res.RowCount)
	}
	t.Logf("SELECT 1 成功，耗时 %dms", res.ElapsedMs)
}

// TestShowDatabases 列出可见数据库（帮助确认要填哪个库名）
func TestShowDatabases(t *testing.T) {
	cfg := loadConfigOrSkip(t)
	st, err := Open(cfg.Database.DSN, cfg.Query.TimeoutSeconds, cfg.Query.MaxRows)
	if err != nil {
		t.Fatalf("连接失败：%v", err)
	}
	res, err := st.Query(context.Background(), "SHOW DATABASES")
	if err != nil {
		t.Fatalf("查询失败：%v", err)
	}
	t.Logf("可见数据库 %d 个：", res.RowCount)
	for _, row := range res.Rows {
		for _, v := range row {
			t.Logf("  - %v", v)
		}
	}
}

// TestWriteRejected 验证写操作被拒绝（只读兜底 + 应用层白名单的实弹验证）
// 注意：run_query 工具会先过 guard 校验，这里直接测 store 层的只读事务是否也拦得住。
func TestWriteRejected(t *testing.T) {
	cfg := loadConfigOrSkip(t)
	st, err := Open(cfg.Database.DSN, cfg.Query.TimeoutSeconds, cfg.Query.MaxRows)
	if err != nil {
		t.Fatalf("连接失败：%v", err)
	}
	// 建一张不存在的临时表来写，语句本身无害（表不存在会先报错），
	// 但只要不是"只读事务拒绝写"的错误也能接受——重点是别真写进去。
	_, err = st.Query(context.Background(), "CREATE TEMPORARY TABLE __probe_should_fail (id INT)")
	if err == nil {
		t.Error("警告：写操作（CREATE）竟然成功了，只读兜底未生效，请检查是否使用了只读账号")
	} else {
		t.Logf("写操作被拒绝（符合预期）：%v", err)
	}
}
