// MySQL 连接与查询执行层
//
// 只读兜底策略（不依赖任何服务端会话变量，兼容代理/中间件/老版本）：
// 每条查询都在只读事务（START TRANSACTION READ ONLY）中执行——写操作会被
// 服务端拒绝，且读写分离代理会将其路由到只读实例。若服务端连只读事务都不支持，
// 降级为普通查询，此时只读仍由应用层语句白名单（guard 层）保证。
package store

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/go-sql-driver/mysql"
)

type Store struct {
	db      *sql.DB
	timeout time.Duration
	maxRows int
	// roTxUnsupported 记录服务端是否不支持只读事务，避免每次都试错一遍
	roTxUnsupported atomic.Bool
}

// Open 打开连接池。不向 DSN 注入任何会话变量，最大化兼容性。
func Open(dsn string, timeoutSeconds, maxRows int) (*Store, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("DSN 解析失败：%w", err)
	}
	cfg.MultiStatements = false // 禁止多语句，显式写死防配置打开
	cfg.ParseTime = true

	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	// 只读查询服务，小连接池即可，避免占用生产库连接数
	db.SetMaxOpenConns(3)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("数据库连接失败：%w", err)
	}
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

// queryer 抽象 *sql.DB 与 *sql.Tx 的共同查询方法
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Query 执行只读查询（SQL 必须已通过 guard 校验），行数超过 maxRows 时截断
func (s *Store) Query(ctx context.Context, sqlText string) (*QueryResult, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	start := time.Now()

	// 优先在只读事务中执行（服务端只读兜底）；不支持时降级为普通查询
	if !s.roTxUnsupported.Load() {
		tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			// 超时/取消等瞬时错误直接返回，不能据此判定服务端不支持只读事务
			if ctx.Err() != nil {
				return nil, fmt.Errorf("开启只读事务失败：%w", err)
			}
			// 代理/老库不支持只读事务：记住并降级，只读由应用层白名单保证
			s.roTxUnsupported.Store(true)
		} else {
			defer tx.Rollback()
			res, qErr := scan(ctx, tx, sqlText, s.maxRows)
			if qErr != nil {
				return nil, qErr
			}
			res.ElapsedMs = time.Since(start).Milliseconds()
			return res, nil
		}
	}
	res, err := scan(ctx, s.db, sqlText, s.maxRows)
	if err != nil {
		return nil, err
	}
	res.ElapsedMs = time.Since(start).Milliseconds()
	return res, nil
}

// scan 执行查询并把结果读进 QueryResult，超过 maxRows 截断
func scan(ctx context.Context, q queryer, sqlText string, maxRows int) (*QueryResult, error) {
	rows, err := q.QueryContext(ctx, sqlText)
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
		if len(result.Rows) >= maxRows {
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
