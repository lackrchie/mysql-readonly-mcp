// 数据库只读查询 MCP 服务（MySQL，stdio 传输）
package main

import (
	"context"
	"flag"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"db-mcp/internal/tools"
)

func main() {
	configPath := flag.String("config", "", "配置文件路径（默认：可执行文件同目录的 config.yaml）")
	flag.Parse()

	server := mcp.NewServer(
		&mcp.Implementation{Name: "db-readonly", Version: "0.1.0"},
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
