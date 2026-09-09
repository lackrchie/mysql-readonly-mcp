# db-mcp — MySQL 只读查询 MCP 服务

让 Claude 直接查询你的 MySQL（CRM）数据库："昨天新增客户多少？按渠道拆一下"——Claude 自己看表结构、写 SQL、执行、解读结果。**只读**：任何写操作在多层防线下都无法通过。

## 项目结构（简单分层，非 DDD）

```
db_mcp/
├── main.go                  # 组装与启动
├── config.example.yaml      # 配置示例（提交 git）
├── config.yaml              # 真实配置（gitignore，含密码，需自行创建）
├── db-mcp                   # 编译产物
└── internal/
    ├── config/              # 配置加载
    ├── guard/               # ★ SQL 安全校验（含 28+ 攻击用例测试）
    ├── store/               # MySQL 连接池与查询执行
    └── tools/               # MCP 工具层（4 个工具）
```

分层判断标准：每层有独立被测试的理由。未采用 DDD——本项目没有领域模型（只读透传 + 安全策略），DDD 的实体/聚合/仓储在此只会是空壳抽象。

## 快速开始

```bash
# 1. 填配置（密码不要经过任何聊天/AI 对话）
cp config.example.yaml config.yaml
vim config.yaml   # 填入 database.dsn

# 2. 编译（改代码后需重新编译）
go build -o db-mcp .

# 3. 注册到 Claude Code（目录级，已完成）
claude mcp add db-readonly -- /Users/yun/Downloads/rust-agent-source/db_mcp/db-mcp

# 4. 在本目录新开会话使用
cd /Users/yun/Downloads/rust-agent-source/db_mcp && claude
# 然后直接问："这个库里有哪些表？" "昨天的订单量是多少？"
```

## 工具

| 工具 | 作用 |
|---|---|
| `list_tables` | 列出所有表（名称、行数估计、注释），支持 LIKE 过滤 |
| `describe_table` | 表结构：列、类型、键、注释 + 索引 |
| `explain_query` | 查看执行计划，跑重查询前评估代价 |
| `run_query` | 执行只读 SQL，超时与行数截断保护 |

## 安全设计（四层防线）

| 层 | 机制 | 位置 | 防什么 |
|---|---|---|---|
| 1 | **会话级只读** `transaction_read_only=1`，代码强制注入 DSN，配置改不掉 | `store/store.go` | 兜底：即使账号有全量权限、即使白名单被绕过，UPDATE/DELETE/DDL 都会被 **MySQL 服务端**拒绝 |
| 2 | 语句白名单：仅 SELECT/SHOW/EXPLAIN/DESCRIBE/WITH；拒多语句与分号；剥离注释防伪装（含 `/*!*/` 版本注释）；WITH 语句检测 CTE 后接写语句 | `guard/guard.go` | prompt 注入诱导写操作 |
| 3 | 显式黑名单：`INTO OUTFILE/DUMPFILE`（写服务器文件，只读事务拦不住）、`LOAD_FILE`（读服务器文件）、`FOR UPDATE`/`LOCK IN SHARE MODE`（加锁阻塞业务）、`SLEEP`/`BENCHMARK`/`GET_LOCK`（拖库） | `guard/guard.go` | 只读事务管不到的危险姿势 |
| 4 | 资源限制：客户端超时 + 服务端 `max_execution_time` + 行数截断 + 连接池上限 3 | `store/store.go` | 大查询拖垮生产库、海量结果撑爆上下文 |
| 5 | **表白名单**（`allowed_tables` 配置）：run_query/explain_query 中引用白名单外的表被拒（含子查询、派生表、JOIN、CTE 体内的引用），list_tables 只展示白名单内的表，describe_table 同样受限 | `guard/tables.go` | 缩小可见面：CRM 库里其他表连读都读不了 |

跑安全测试：`go test ./internal/guard/ -v`

### 仍然建议：建只读账号

应用层与会话层防线之外，权限层兜底只需两条 SQL（DBA 权限执行）：

```sql
CREATE USER 'crm_readonly'@'%' IDENTIFIED BY '强密码';
GRANT SELECT ON your_crm_db.* TO 'crm_readonly'@'%';
```

然后把 config.yaml 的 DSN 换成这个账号。三道锁总比两道稳。

## 使用示例

- "这个库有哪些表？挑几个核心的给我讲讲结构"
- "昨天新增了多少客户？"
- "近 30 天每天的成交金额，按销售拆分"
- "帮我找出重复的客户手机号"
