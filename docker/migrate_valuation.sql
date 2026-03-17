-- =============================================================
-- 估值分位模块 (Valuation Module) — 数据库迁移脚本
-- 执行方式：
--   docker exec -i stock_db psql -U admin -d stock_system \
--     < docker/migrate_valuation.sql
--
-- 设计说明：
--   stock_valuations  — 每只股票的最新估值快照（1行/股票，Upsert）
--   stock_valuation_history — 每日历史记录（用于计算分位，1行/股票/日，累积）
-- =============================================================

-- ── 最新估值快照（供接口实时返回）────────────────────────────────
CREATE TABLE IF NOT EXISTS stock_valuations (
    stock_code    VARCHAR(10)   PRIMARY KEY,
    stock_name    VARCHAR(50)   NOT NULL DEFAULT '',
    pe_ttm        NUMERIC(12,4),             -- 市盈率 TTM（可为负，负值表示亏损）
    pb            NUMERIC(12,4),             -- 市净率
    pe_percentile NUMERIC(6,2),              -- PE 历史分位 (0-100)，NULL 表示历史数据不足
    pb_percentile NUMERIC(6,2),              -- PB 历史分位 (0-100)
    history_days  INT           DEFAULT 0,   -- 已积累的历史天数
    updated_at    TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

-- ── 历史每日记录（积累用于分位计算）──────────────────────────────
CREATE TABLE IF NOT EXISTS stock_valuation_history (
    id          BIGSERIAL    PRIMARY KEY,
    stock_code  VARCHAR(10)  NOT NULL,
    trade_date  DATE         NOT NULL,
    pe_ttm      NUMERIC(12,4),
    pb          NUMERIC(12,4),
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    UNIQUE (stock_code, trade_date)
);

CREATE INDEX IF NOT EXISTS idx_val_hist_code_date
    ON stock_valuation_history (stock_code, trade_date DESC);

-- 验证
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'stock_valuations') THEN
        RAISE NOTICE '✅ stock_valuations 表创建成功';
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'stock_valuation_history') THEN
        RAISE NOTICE '✅ stock_valuation_history 表创建成功';
    END IF;
END $$;
