// MySQL 连接与查询执行层
//
// 安全兜底：无论用户配置的 DSN 写了什么，这里都强制注入
// transaction_read_only=1（会话级只读）——写操作会被 MySQL 服务端直接拒绝，
// 即使连接账号拥有全量权限、即使应用层白名单被绕过。
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/go-sql-driver/mysql"
)

type Store struct {
	db      *sql.DB
	timeout time.Duration
	maxRows int
}

// Open 打开连接池；对 DSN 强制附加只读与安全相关参数
func Open(dsn string, timeoutSeconds, maxRows int) (*Store, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("DSN 解析失败：%w", err)
	}
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	// ★ 会话级只读：本服务的核心兜底，代码强制，配置改不掉
	cfg.Params["transaction_read_only"] = "1"
	// SELECT 服务端超时（毫秒），比客户端超时略短，双保险
	cfg.Params["max_execution_time"] = fmt.Sprintf("%d", timeoutSeconds*1000)
	// 禁止多语句（驱动默认即 false，显式写死防配置打开）
	cfg.MultiStatements = false
	cfg.ParseTime = true

	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	// 只读查询服务，小连接池即可，避免占用生产库连接数
	db.SetMaxOpenConns(3)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)
	return &Store{
		db:      db,
		timeout: time.Duration(timeoutSeconds) * time.Second,
		maxRows: maxRows,
	}, nil
}

// QueryResult 查询结果：列名 + 行数据 + 截断标记
type QueryResult struct {
	Columns   []string         `json:"columns"`
	Rows      []map[string]any `json:"rows"`
	RowCount  int              `json:"row_count"`
	Truncated bool             `json:"truncated"`
	ElapsedMs int64            `json:"elapsed_ms"`
}

// Query 执行只读查询（SQL 必须已通过 guard 校验），行数超过 maxRows 时截断
func (s *Store) Query(ctx context.Context, sqlText string) (*QueryResult, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	start := time.Now()
	rows, err := s.db.QueryContext(ctx, sqlText)
	if err != nil {
		return nil, fmt.Errorf("查询执行失败：%w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	result := &QueryResult{Columns: cols, Rows: []map[string]any{}}
	values := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range values {
		ptrs[i] = &values[i]
	}
	for rows.Next() {
		if len(result.Rows) >= s.maxRows {
			result.Truncated = true
			break
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			row[c] = normalize(values[i])
		}
		result.Rows = append(result.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result.RowCount = len(result.Rows)
	result.ElapsedMs = time.Since(start).Milliseconds()
	return result, nil
}

// normalize 把驱动返回的 []byte 等类型转成可 JSON 序列化的值
func normalize(v any) any {
	switch t := v.(type) {
	case []byte:
		return string(t)
	case time.Time:
		return t.Format("2006-01-02 15:04:05")
	default:
		return v
	}
}

// Ping 连接测试
func (s *Store) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	return s.db.PingContext(ctx)
}
