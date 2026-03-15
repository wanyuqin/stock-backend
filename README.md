# 个人 A 股分析系统 — 后端

> Go (Gin) + PostgreSQL + Redis 后端服务

## 项目结构

```
stock-backend/
├── cmd/server/main.go               # 程序入口，优雅启动/关闭
├── internal/
│   ├── config/config.go             # 配置加载（env / .env）
│   ├── api/
│   │   ├── middleware/cors.go       # CORS 跨域中间件
│   │   ├── middleware/logger.go     # Zap 请求日志中间件
│   │   ├── handler/health.go        # /health  /readyz 探针
│   │   └── router/router.go         # 路由注册
│   ├── model/stock.go               # 数据模型定义
│   ├── repo/interfaces.go           # Repository 接口
│   └── service/stock.go             # 业务逻辑层
├── pkg/logger/logger.go             # Zap logger 工厂
├── docker/init.sql                  # 数据库初始化 SQL
├── docker-compose.yml               # PostgreSQL + Redis + pgAdmin
├── .env.example                     # 环境变量模板
└── Makefile
```

## 快速启动

```bash
# 1. 启动基础服务
make docker-up

# 2. 拉取 Go 依赖
go mod tidy

# 3. 运行后端
make run
```

服务启动后访问：
- API 健康检查：http://localhost:8888/health
- pgAdmin：http://localhost:8080（admin@admin.com / admin）

## 环境变量

参考 `.env.example` 复制为 `.env` 后修改。


docker exec -i stock_db psql -U admin -d stock_system < docker