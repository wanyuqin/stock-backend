.PHONY: run build tidy lint test docker-up docker-down

## run: 启动开发服务器（自动读取 .env）
run:
	go run ./cmd/server/...

## build: 编译二进制到 bin/server
build:
	go build -o bin/server ./cmd/server/...

## tidy: 整理依赖
tidy:
	go mod tidy

## lint: 静态检查（需安装 golangci-lint）
lint:
	go run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.60.1 run ./...

## test: 运行所有测试
test:
	go test -v -race ./...

## docker-up: 启动 PostgreSQL / Redis / pgAdmin
docker-up:
	docker compose up -d

## docker-down: 停止容器（保留数据卷）
docker-down:
	docker compose down


# 自动执行最新的 SQL 迁移文件
db-migrate:
	@latest_sql=$$(ls -t docker/*.sql | head -1); \
	if [ -z "$$latest_sql" ]; then echo "No SQL files found in docker/"; exit 1; fi; \
	echo "Applying: $$latest_sql ..."; \
	docker exec -i stock_db psql -U admin -d stock_system < $$latest_sql

# 验证当前数据库表结构
db-status:
	docker exec -it stock_db psql -U admin -d stock_system -c "\dt"