// 数据库只读查询 MCP 服务（MySQL，stdio 传输）
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"db-mcp/internal/config"
	"db-mcp/internal/store"
	"db-mcp/internal/tools"
)

func main() {
	configPath := flag.String("config", "", "配置文件路径（默认：可执行文件同目录的 config.yaml）")
	testConn := flag.Bool("test", false, "仅做连接诊断后退出（不启动 MCP 服务）")
	flag.Parse()

	// 连接诊断模式：读取配置、探测数据库能力、打印报告
	if *testConn {
		cfg, err := config.Load(*configPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Print(store.Diagnose(cfg.Database.DSN, cfg.Query.TimeoutSeconds))
		return
	}

	server := mcp.NewServer(
		&mcp.Implementation{Name: "db-crm-readonly", Version: "0.1.0"},
		&mcp.ServerOptions{
			Instructions: "这是一个 MySQL 只读查询服务。探索流程：list_tables 看有哪些表 → " +
				"describe_table 看表结构 → explain_query 评估查询代价 → run_query 执行查询。" +
				"只允许只读语句，写操作会被拒绝。结果被截断时应缩小查询范围。",
		},
	)
	tools.Register(server, &tools.Deps{ConfigPath: *configPath})

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("服务退出：%v", err)
	}
}
