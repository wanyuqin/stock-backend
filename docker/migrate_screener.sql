-- =============================================================
-- 迁移：全市场每日快照宽表（量化筛选器专用）
-- 执行方式：
--   docker exec -i stock_db psql -U admin -d stock_system \
--     < docker/migrate_screener.sql
-- =============================================================

CREATE TABLE IF NOT EXISTS stock_daily_snapshots (
    id               BIGSERIAL     PRIMARY KEY,
    trade_date       DATE          NOT NULL DEFAULT CURRENT_DATE,
    code             VARCHAR(10)   NOT NULL,
    name             VARCHAR(50),

    -- 基础行情
    price            NUMERIC(12,2),
    pct_chg          NUMERIC(8,2),
    turnover_rate    NUMERIC(8,2),
    vol_ratio        NUMERIC(8,2),

    -- 资金因子
    main_inflow      NUMERIC(18,2),
    main_inflow_pct  NUMERIC(8,2),

    -- 技术因子
    ma5              NUMERIC(12,2),
    ma20             NUMERIC(12,2),
    is_multi_aligned BOOLEAN,
    bias_20          NUMERIC(8,2),

    -- 时间戳
    created_at       TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ   NOT NULL DEFAULT NOW(),

    UNIQUE(trade_date, code)
);

CREATE INDEX IF NOT EXISTS idx_snapshot_date
    ON stock_daily_snapshots (trade_date DESC);

CREATE INDEX IF NOT EXISTS idx_snapshot_score_lookup
    ON stock_daily_snapshots (trade_date, main_inflow_pct DESC, pct_chg DESC);

CREATE INDEX IF NOT EXISTS idx_snapshot_aligned
    ON stock_daily_snapshots (trade_date, is_multi_aligned)
    WHERE is_multi_aligned = TRUE;

-- 如果表已存在但缺少 created_at / updated_at，补列
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'stock_daily_snapshots' AND column_name = 'created_at'
    ) THEN
        ALTER TABLE stock_daily_snapshots ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'stock_daily_snapshots' AND column_name = 'updated_at'
    ) THEN
        ALTER TABLE stock_daily_snapshots ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
    END IF;
END $$;

SELECT tablename FROM pg_tables
WHERE schemaname = 'public' AND tablename = 'stock_daily_snapshots';
