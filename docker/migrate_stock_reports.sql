-- =============================================================
-- 研报情报站 (Stock Reports) — 数据库迁移脚本
-- 执行方式：
--   docker exec -i stock_db psql -U admin -d stock_system \
--     < docker/migrate_stock_reports.sql
-- =============================================================

CREATE TABLE IF NOT EXISTS stock_reports (
    id           BIGSERIAL     PRIMARY KEY,
    info_code    VARCHAR(40)   UNIQUE NOT NULL,   -- 东财研报唯一 ID，幂等去重
    stock_code   VARCHAR(10)   NOT NULL,          -- 股票代码
    stock_name   VARCHAR(20),                     -- 股票名称
    title        TEXT          NOT NULL,           -- 研报标题
    org_name     VARCHAR(100),                    -- 机构全称
    org_sname    VARCHAR(50),                     -- 机构简称
    rating_name  VARCHAR(20),                     -- 评级，如 买入/增持
    publish_date TIMESTAMPTZ   NOT NULL,          -- 发布时间
    detail_url   VARCHAR(512),                    -- 详情页地址
    ai_summary   TEXT,                            -- AI 生成的摘要（初始为空）
    is_processed BOOLEAN       DEFAULT FALSE,     -- 是否已完成 AI 摘要
    created_at   TIMESTAMPTZ   DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_stock_reports_stock_code
    ON stock_reports (stock_code);

CREATE INDEX IF NOT EXISTS idx_stock_reports_publish_date
    ON stock_reports (publish_date DESC);

CREATE INDEX IF NOT EXISTS idx_stock_reports_unprocessed
    ON stock_reports (is_processed)
    WHERE is_processed = FALSE;

-- 验证
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_name = 'stock_reports'
    ) THEN
        RAISE NOTICE '✅ stock_reports 表创建成功';
    END IF;
END $$;
