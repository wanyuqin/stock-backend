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
