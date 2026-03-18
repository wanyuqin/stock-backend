-- ═══════════════════════════════════════════════════════════════
-- 板块映射缓存表
-- 执行：make db-migrate
-- ═══════════════════════════════════════════════════════════════

-- ── 行业板块基础信息表 ────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS sectors (
    code        VARCHAR(20)   PRIMARY KEY,   -- 板块代码，如 BK0726
    name        VARCHAR(100)  NOT NULL,      -- 板块名称，如 印制电路板
    market_id   INT           NOT NULL DEFAULT 90,  -- 东财市场ID，板块固定为90
    created_at  TIMESTAMPTZ   DEFAULT NOW(),
    updated_at  TIMESTAMPTZ   DEFAULT NOW()
);

-- ── 个股-板块映射缓存表 ──────────────────────────────────────────
-- 记录每只股票对应的主行业板块（取 slist/get 返回的第一个）
CREATE TABLE IF NOT EXISTS stock_sector_relations (
    stock_code   VARCHAR(10)   PRIMARY KEY,   -- 个股代码
    sector_code  VARCHAR(20)   NOT NULL,      -- 板块代码 FK
    sector_name  VARCHAR(100)  NOT NULL,      -- 冗余存储板块名，避免 JOIN
    synced_at    TIMESTAMPTZ   DEFAULT NOW()  -- 最后同步时间
);

CREATE INDEX IF NOT EXISTS idx_ssr_sector_code ON stock_sector_relations (sector_code);

-- ── 幂等补列（若部分字段已存在则跳过）──────────────────────────
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name='sectors' AND column_name='market_id'
    ) THEN
        ALTER TABLE sectors ADD COLUMN market_id INT NOT NULL DEFAULT 90;
    END IF;
END $$;
