-- =============================================================
-- 复盘引擎 (Audit Engine) — 数据库迁移脚本
-- 执行方式：
--   docker exec -i stock_db psql -U admin -d stock_system \
--     < docker/migrate_audit_engine.sql
-- =============================================================

-- ── Step 1：为 trade_logs 补充买卖分离的理由字段 ──────────────
-- 原 reason 字段保留做兼容，新增 buy_reason / sell_reason
ALTER TABLE trade_logs
    ADD COLUMN IF NOT EXISTS buy_reason  TEXT DEFAULT '',
    ADD COLUMN IF NOT EXISTS sell_reason TEXT DEFAULT '';

-- 将存量 reason 回填到 buy_reason（兼容旧数据）
UPDATE trade_logs
SET buy_reason = reason
WHERE action = 'BUY'
  AND buy_reason = ''
  AND reason != '';

UPDATE trade_logs
SET sell_reason = reason
WHERE action = 'SELL'
  AND sell_reason = ''
  AND reason != '';

-- ── Step 2：创建 trade_reviews 主表 ──────────────────────────
CREATE TABLE IF NOT EXISTS trade_reviews (
    id               BIGSERIAL    PRIMARY KEY,
    -- 关联的卖出交易记录（1:1）
    trade_log_id     BIGINT       UNIQUE NOT NULL
                         REFERENCES trade_logs(id) ON DELETE CASCADE,
    stock_code       VARCHAR(10)  NOT NULL,

    -- 卖出后价格追踪
    price_at_sell    NUMERIC(15,4),          -- 卖出当日收盘价（确认价）
    price_1d_after   NUMERIC(15,4),          -- 卖出后第 1 个交易日收盘
    price_3d_after   NUMERIC(15,4),          -- 卖出后第 3 个交易日收盘
    price_5d_after   NUMERIC(15,4),          -- 卖出后第 5 个交易日收盘
    max_price_5d     NUMERIC(15,4),          -- 卖出后 5 日内的最高价（用于后悔指数）

    -- 量化审计指标
    pnl_pct          NUMERIC(8,4),           -- 本次交易盈亏百分比（含手续费）
    post_5d_gain_pct NUMERIC(8,4),           -- 卖出后 5 日涨幅：(price_5d_after-price_at_sell)/price_at_sell
    regret_index     NUMERIC(8,4),           -- 后悔指数：(max_price_5d-price_at_sell)/price_at_sell
    execution_score  INT CHECK (execution_score BETWEEN 0 AND 100),

    -- 逻辑一致性审计
    consistency_flag VARCHAR(30) DEFAULT 'NORMAL',
    -- 枚举值: NORMAL | LOGIC_CONFLICT | CHASING_HIGH | PANIC_SELL | PREMATURE_EXIT
    consistency_note TEXT        DEFAULT '',

    -- 主观/心理标注（用户手动填写）
    mental_state     VARCHAR(50) DEFAULT '',
    -- 枚举建议: 冷静 | 贪婪 | 恐惧 | 急躁 | 犹豫 | 自信 | 迷茫
    user_note        TEXT        DEFAULT '',  -- 用户自己的主观复盘文字
    tags             JSONB       DEFAULT '[]'::JSONB,
    -- 示例: ["卖早了","完美逃顶","违背逻辑","追涨杀跌"]

    -- AI 审计结论
    ai_audit_comment TEXT        DEFAULT '',  -- AI 复盘总结（毒舌模式）
    improvement_plan TEXT        DEFAULT '',  -- 改进建议

    -- 追踪状态机
    tracking_status  VARCHAR(20) DEFAULT 'PENDING',
    -- PENDING → 等待价格追踪；PARTIAL → 部分数据已填充；COMPLETED → 5日追踪完毕
    ai_generated_at  TIMESTAMPTZ,            -- AI 审计生成时间

    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ── Step 3：索引 ──────────────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_reviews_stock_code
    ON trade_reviews (stock_code);

CREATE INDEX IF NOT EXISTS idx_reviews_tracking_status
    ON trade_reviews (tracking_status)
    WHERE tracking_status != 'COMPLETED';

CREATE INDEX IF NOT EXISTS idx_reviews_created_at
    ON trade_reviews (created_at DESC);

CREATE INDEX IF NOT EXISTS idx_reviews_mental_state
    ON trade_reviews (mental_state)
    WHERE mental_state != '';

CREATE INDEX IF NOT EXISTS idx_trade_logs_buy_reason
    ON trade_logs (stock_code, action, traded_at DESC);

-- ── Step 4：自动更新 updated_at 触发器 ────────────────────────
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_reviews_updated_at ON trade_reviews;
CREATE TRIGGER trg_reviews_updated_at
    BEFORE UPDATE ON trade_reviews
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- ── Step 5：验证 ──────────────────────────────────────────────
DO $$
BEGIN
    -- 确认 trade_reviews 表存在
    IF EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_name = 'trade_reviews'
    ) THEN
        RAISE NOTICE '✅ trade_reviews 表创建成功';
    END IF;

    -- 确认 trade_logs 新字段存在
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'trade_logs' AND column_name = 'buy_reason'
    ) THEN
        RAISE NOTICE '✅ trade_logs.buy_reason 字段添加成功';
    END IF;

    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'trade_logs' AND column_name = 'sell_reason'
    ) THEN
        RAISE NOTICE '✅ trade_logs.sell_reason 字段添加成功';
    END IF;
END $$;
