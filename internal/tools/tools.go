// MCP 工具层：4 个只读查询工具
package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"db-mcp/internal/config"
	"db-mcp/internal/guard"
	"db-mcp/internal/store"
)

// Deps 工具依赖：配置立即加载，数据库懒加载
// （懒加载让服务在 config.yaml 未就绪时也能完成 MCP 握手，工具调用时给出可操作的错误提示）
type Deps struct {
	ConfigPath string

	mu  sync.Mutex
	cfg *config.Config
	st  *store.Store
}

func (d *Deps) get() (*store.Store, *config.Config, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.st != nil {
		return d.st, d.cfg, nil
	}
	cfg, err := config.Load(d.ConfigPath)
	if err != nil {
		return nil, nil, err
	}
	st, err := store.Open(cfg.Database.DSN, cfg.Query.TimeoutSeconds, cfg.Query.MaxRows)
	if err != nil {
		return nil, nil, err
	}
	d.cfg, d.st = cfg, st
	return st, cfg, nil
}

// ==================== 参数定义 ====================

type ListTablesArgs struct {
	Pattern string `json:"pattern,omitempty" jsonschema:"表名过滤（LIKE 模式，如 'user%'），可选"`
}

type DescribeTableArgs struct {
	Table string `json:"table" jsonschema:"表名"`
}

type RunQueryArgs struct {
	SQL string `json:"sql" jsonschema:"只读 SQL（仅允许 SELECT/SHOW/EXPLAIN/DESCRIBE/WITH），结果行数有上限，超出会截断"`
}

type ExplainQueryArgs struct {
	SQL string `json:"sql" jsonschema:"要分析执行计划的 SELECT 语句（不实际执行数据扫描）"`
}

// ==================== 工具注册 ====================

func Register(server *mcp.Server, deps *Deps) {
	roAnno := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}

	mcp.AddTool(server, &mcp.Tool{
		Name: "list_tables",
		Description: "列出数据库中的表（表名、行数估计、注释）。这是探索数据库的起点。" +
			"可用 pattern 参数按 LIKE 模式过滤表名。",
		Annotations: roAnno,
	}, func(ctx context.Context, req *mcp.CallToolRequest, args ListTablesArgs) (*mcp.CallToolResult, *store.QueryResult, error) {
		st, cfg, err := deps.get()
		if err != nil {
			return nil, nil, err
		}
		sqlText := "SELECT table_name AS `table`, table_rows AS estimated_rows, table_comment AS comment " +
			"FROM information_schema.tables WHERE table_schema = DATABASE()"
		if args.Pattern != "" {
			// pattern 经参数化传入不可行（information_schema 查询已固定），做保守转义
			sqlText += fmt.Sprintf(" AND table_name LIKE '%s'", escapeLike(args.Pattern))
		}
		// 配置了表白名单时，只展示白名单内的表
		if len(cfg.Security.AllowedTables) > 0 {
			sqlText += " AND table_name IN (" + quoteList(cfg.Security.AllowedTables) + ")"
		}
		sqlText += " ORDER BY table_name"
		res, err := st.Query(ctx, sqlText)
		_ = cfg
		return nil, res, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "describe_table",
		Description: "查看某张表的结构：列名、类型、是否可空、键、默认值、注释，以及索引信息。" +
			"写 SQL 前先用它了解表结构。",
		Annotations: roAnno,
	}, func(ctx context.Context, req *mcp.CallToolRequest, args DescribeTableArgs) (*mcp.CallToolResult, map[string]*store.QueryResult, error) {
		st, cfg, err := deps.get()
		if err != nil {
			return nil, nil, err
		}
		if err := guard.CheckTables("SELECT 1 FROM "+args.Table, cfg.Security.AllowedTables); err != nil {
			return nil, nil, err
		}
		table := escapeLike(args.Table)
		columns, err := st.Query(ctx,
			"SELECT column_name, column_type, is_nullable, column_key, column_default, column_comment "+
				"FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = '"+table+"' "+
				"ORDER BY ordinal_position")
		if err != nil {
			return nil, nil, err
		}
		if len(columns.Rows) == 0 {
			return nil, nil, fmt.Errorf("表 %s 不存在，可先用 list_tables 查看可用的表", args.Table)
		}
		indexes, err := st.Query(ctx,
			"SELECT index_name, GROUP_CONCAT(column_name ORDER BY seq_in_index) AS columns, "+
				"MAX(non_unique) = 0 AS is_unique "+
				"FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = '"+table+"' "+
				"GROUP BY index_name")
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]*store.QueryResult{"columns": columns, "indexes": indexes}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "run_query",
		Description: "执行只读 SQL 查询并返回结果。仅允许 SELECT/SHOW/EXPLAIN/DESCRIBE/WITH，" +
			"禁止任何写操作、多语句、加锁与文件操作；查询有超时与行数上限，结果超限会截断（truncated=true），" +
			"此时应加 WHERE/LIMIT 缩小范围。",
		Annotations: roAnno,
	}, func(ctx context.Context, req *mcp.CallToolRequest, args RunQueryArgs) (*mcp.CallToolResult, *store.QueryResult, error) {
		st, cfg, err := deps.get()
		if err != nil {
			return nil, nil, err
		}
		cleaned, err := guard.Validate(args.SQL, cfg.Security.AllowedStatements)
		if err != nil {
			return nil, nil, err
		}
		if err := guard.CheckTables(cleaned, cfg.Security.AllowedTables); err != nil {
			return nil, nil, err
		}
		res, err := st.Query(ctx, cleaned)
		return nil, res, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "explain_query",
		Description: "查看 SELECT 语句的执行计划（EXPLAIN），不实际扫描数据。" +
			"跑可能很重的查询前先用它评估代价（关注 type/rows/key 列）。",
		Annotations: roAnno,
	}, func(ctx context.Context, req *mcp.CallToolRequest, args ExplainQueryArgs) (*mcp.CallToolResult, *store.QueryResult, error) {
		st, cfg, err := deps.get()
		if err != nil {
			return nil, nil, err
		}
		cleaned, err := guard.Validate(args.SQL, cfg.Security.AllowedStatements)
		if err != nil {
			return nil, nil, err
		}
		if err := guard.CheckTables(cleaned, cfg.Security.AllowedTables); err != nil {
			return nil, nil, err
		}
		res, err := st.Query(ctx, "EXPLAIN "+cleaned)
		return nil, res, err
	})
}

// quoteList 把表名列表转成 SQL IN 子句内容（'a','b'），表名做保守清洗
func quoteList(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, n := range names {
		quoted = append(quoted, "'"+escapeLike(n)+"'")
	}
	return strings.Join(quoted, ",")
}

// escapeLike 对拼入 SQL 的标识符/模式做保守转义（仅允许安全字符集之外的字符被剔除）
func escapeLike(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '_' || r == '%' || r == '-' || r == '$' {
			out = append(out, r)
		}
	}
	return string(out)
}
