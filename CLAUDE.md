# 项目：个人 A股分析系统 (Terminal v0.1)

## 1. 技术栈
- **语言**: Go 1.24 (利用最新特性，如泛型、原生并发控制)
- **Web 框架**: Gin
- **持久化**: PostgreSQL + GORM
- **缓存**: Redis (用于行情快照、Token 存储)
- **日志**: Zap Logger (结构化日志)
- **外部数据**: 东方财富 API (爬虫/接口采集)

## 2. 项目结构
```
stock-backend/
├── cmd/server/main.go               # 程序入口，处理优雅启动/关闭
├── internal/
│   ├── config/config.go             # 环境驱动配置加载
│   ├── api/
│   │   ├── middleware/              # CORS, Logger, Auth 等中间件
│   │   ├── handler/                 # 接口层，解析参数，不含业务逻辑
│   │   └── router/                  # 路由注册与版本控制
│   ├── model/                       # GORM 数据模型与 DTO 定义
│   ├── repo/                        # 数据访问层 (DAO)，抽象接口
│   └── service/                     # 核心业务逻辑层 (Service)
├── pkg/                             # 通用工具包 (Logger, HTTPClient, Errors)
├── docker/                          # 部署与初始化 SQL
├── .env.example                     # 环境变量模板
└── Makefile                         # 常用开发指令
```

## 3. 编码规范 (Coding Standards)

### 3.1 封装与解耦
- **接口驱动**: `service` 层必须通过 `repo/interfaces.go` 调用数据层，禁止直接依赖具体的数据库实现。
- **构造函数注入**: 必须使用 `NewXXX` 构造函数进行依赖注入 (DI)。
- **单一职责**: `handler` 只处理 HTTP 相关逻辑，`service` 处理业务流程，`repo` 只负责 SQL 执行。

### 3.2 错误处理
- **显式返回**: 禁止使用 `panic`。所有方法必须返回 `error`。
- **错误包装**: 使用 `fmt.Errorf("context: %w", err)` 保持错误堆栈。
- **统一响应**: 必须使用项目定义的 `pkg/response` 封装 JSON 响应，包含 `code`, `data`, `msg`。

### 3.3 并发与性能
- **Context 控制**: 所有方法第一个参数必须是 `ctx context.Context`。
- **并发安全**: 爬虫任务必须有 `RateLimiter`，并妥善处理 `WaitGroup` 或 `Channel`。

## 4. 业务核心逻辑 (Business Logic)

### 4.1 爬虫自愈机制 (Self-Healing)
- **Token 管理**: 封装 `TokenManager`，自动从东财首页提取 `qgssid` 并持久化至 Redis。
- **状态感知**: 若接口返回 `502`、`403` 或 `Token Expired`，系统必须自动触发 Token 刷新并重试当前任务。

### 4.2 数据更新策略
- **实时行情**: 针对自选股进行高频轮询。
- **研报采集**: 每日收盘后同步。仅保留“买入/增持”评级，并集成 LLM 提取摘要。
- **估值分位**: 每日 16:30 调用东财 F10 接口，基于过去 3-5 年 PE-TTM 序列计算当前分位点。

## 5. 数据库设计原则
- **命名**: 字段使用 `snake_case`，必须包含 `created_at`, `updated_at`。
- **幂等性**: 采集任务入库必须使用 `Upsert` (ON CONFLICT DO UPDATE) 逻辑。
- **性能**: 针对 `stock_code` 和 `publish_date` 建立复合索引。

## 6. 开发路线图 (Roadmap)
- [x] 基础框架搭建 (Gin + Gorm + Zap)
- [x] 实时行情抓取与仪表盘展示
- [x] 研报情报站基础版 (数据采集与入库)
- [ ] **(进行中) 研报 AI 摘要集成 (Claude API)**
- [ ] **(待开始) 个股估值分位计算模块 (基于东财 F10)**
- [ ] **(待开始) 交易日志与归因分析系统**