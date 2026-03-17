
---
name: SQL迁移&Schema管理
description:  SQL Migration & Schema Management for project
---

# Skill: SQL Migration & Schema Management

## Context
项目使用 Docker 容器 `stock_db` 运行 PostgreSQL。所有 DDL 变更必须记录在 `docker/` 目录下的 SQL 文件中。

## Protocol (操作规程)
1. **生成变更**:
    - 当需要修改数据库结构时，先在 `docker/` 下创建一个带时间戳的 SQL 文件。
    - 文件命名格式: `YYYYMMDD_HHMM_description.sql`。
2. **执行迁移**:
    - 文件写入磁盘后，必须**自动执行** `make db-migrate` 命令。
    - 严禁要求用户手动执行 docker exec 命令。
3. **验证结果**:
    - 迁移完成后，运行 `make db-status` 确认表已更新。
4. **同步代码**:
    - 确认数据库变更成功后，同步更新 `internal/model/` 下的 GORM 结构体。

## Safety Checks
- 执行迁移前，先检查 SQL 语法是否正确。
- 严禁执行任何会清空生产数据 (DROP TABLE / TRUNCATE) 的指令，除非用户明确要求。