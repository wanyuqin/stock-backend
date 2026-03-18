-- ── 买入计划表（迁移脚本，可在已有数据库上执行）────────────────
-- 执行方式：
--   docker exec -i stock_db psql -U admin -d stock_system < docker/migrate_buy_plans.sql

CREATE TABLE IF NOT EXISTS buy_plans (
    id                  BIGSERIAL      PRIMARY KEY,
    user_id             BIGINT         NOT NULL DEFAULT 1,
    stock_code          VARCHAR(10)    NOT NULL,
    stock_name          VARCHAR(50)    NOT NULL DEFAULT '',

    -- 核心买入价区间
    buy_price           NUMERIC(12,4),                      -- 预期买入价（NULL = 市价）
    buy_price_high      NUMERIC(12,4),                      -- 买入价区间上限
    target_price        NUMERIC(12,4),                      -- 目标价（止盈位）
    stop_loss_price     NUMERIC(12,4),                      -- 止损价

    -- 仓位计划
    planned_volume      INT            NOT NULL DEFAULT 0,  -- 计划股数
    planned_amount      NUMERIC(15,2),                      -- 计划金额（元）
    position_ratio      NUMERIC(5,2),                       -- 仓位占比（%）
    buy_batches         INT            NOT NULL DEFAULT 1,  -- 分批次数

    -- 策略理由
    reason              TEXT           NOT NULL DEFAULT '',
    catalyst            TEXT           NOT NULL DEFAULT '',

    -- 触发条件（JSONB）
    trigger_conditions  JSONB          NOT NULL DEFAULT '{}',

    -- 预期收益测算
    expected_return_pct NUMERIC(8,2),                       -- 预期收益率（%）
    risk_reward_ratio   NUMERIC(8,2),                       -- 盈亏比

    -- 有效期
    valid_until         TIMESTAMPTZ,

    -- 状态机
    status              VARCHAR(20)    NOT NULL DEFAULT 'WATCHING'
                            CHECK (status IN ('WATCHING','READY','EXECUTED','ABANDONED','EXPIRED')),
    executed_at         TIMESTAMPTZ,
    trade_log_id        BIGINT,

    created_at          TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ    NOT NULL DEFAULT NOW()
);

-- 索引
CREATE INDEX IF NOT EXISTS idx_buy_plans_user        ON buy_plans (user_id);
CREATE INDEX IF NOT EXISTS idx_buy_plans_stock       ON buy_plans (stock_code);
CREATE INDEX IF NOT EXISTS idx_buy_plans_status      ON buy_plans (status);
CREATE INDEX IF NOT EXISTS idx_buy_plans_user_status ON buy_plans (user_id, status);
CREATE INDEX IF NOT EXISTS idx_buy_plans_valid       ON buy_plans (valid_until) WHERE valid_until IS NOT NULL;
