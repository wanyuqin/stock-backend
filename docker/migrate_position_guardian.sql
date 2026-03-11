-- ═══════════════════════════════════════════════════════════════
-- 持仓守护系统迁移脚本
-- 执行：docker exec -i stock_db psql -U admin -d stock_system < docker/migrate_position_guardian.sql
-- ═══════════════════════════════════════════════════════════════

-- ── 持仓明细表 ────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS position_details (
    id               BIGSERIAL      PRIMARY KEY,
    stock_code       VARCHAR(10)    NOT NULL,
    avg_cost         NUMERIC(15,4)  NOT NULL,           -- 持仓均价（含买入手续费）
    quantity         INT            NOT NULL,           -- 总持仓数量（股）
    available_qty    INT            NOT NULL DEFAULT 0, -- 当日可用数量（T+0 卖出）
    hard_stop_loss   NUMERIC(15,4),                     -- Cost - 2×ATR 硬止损位
    updated_at       TIMESTAMPTZ    DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_position_code ON position_details (stock_code);

-- ── 诊断快照表 ────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS position_diagnostics (
    id               BIGSERIAL      PRIMARY KEY,
    stock_code       VARCHAR(10)    NOT NULL,
    signal_type      VARCHAR(20),                       -- HOLD | SELL | BUY_T | SELL_T | STOP_LOSS
    action_directive TEXT,                              -- AI 生成的一句话行动指令
    data_snapshot    JSONB,                             -- ATR / MA20 / PnL 等当时数据
    created_at       TIMESTAMPTZ    DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_diag_code_time ON position_diagnostics (stock_code, created_at DESC);

-- ── 补列（若表已存在但缺少字段）─────────────────────────────────────
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name='position_details' AND column_name='available_qty'
    ) THEN
        ALTER TABLE position_details ADD COLUMN available_qty INT NOT NULL DEFAULT 0;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name='position_details' AND column_name='hard_stop_loss'
    ) THEN
        ALTER TABLE position_details ADD COLUMN hard_stop_loss NUMERIC(15,4);
    END IF;
END $$;
