-- =============================================================
-- 迁移：资金流向 & 异动告警模块
-- 执行方式（对运行中的 DB）：
--   docker exec -i <容器名> psql -U admin -d stock_system \
--     < docker/migrate_money_flow.sql
-- =============================================================

-- ── 1. stocks 表新增 latest_money_flow 列 ─────────────────────
ALTER TABLE stocks
  ADD COLUMN IF NOT EXISTS latest_money_flow NUMERIC(15,2);

-- ── 2. 资金流向日志表 ─────────────────────────────────────────
-- 每次轮询写入一条快照，保留历史便于回溯趋势
CREATE TABLE IF NOT EXISTS money_flow_logs (
    id                    BIGSERIAL      PRIMARY KEY,
    stock_code            VARCHAR(10)    NOT NULL,
    date                  DATE           NOT NULL DEFAULT CURRENT_DATE,
    main_net_inflow       NUMERIC(15,2)  NOT NULL DEFAULT 0,  -- 主力净流入 (元)
    super_large_inflow    NUMERIC(15,2)  NOT NULL DEFAULT 0,  -- 超大单净流入
    large_inflow          NUMERIC(15,2)  NOT NULL DEFAULT 0,  -- 大单净流入
    medium_inflow         NUMERIC(15,2)  NOT NULL DEFAULT 0,  -- 中单净流入
    small_inflow          NUMERIC(15,2)  NOT NULL DEFAULT 0,  -- 小单净流入
    main_inflow_pct       NUMERIC(8,4)   NOT NULL DEFAULT 0,  -- 主力净流入占比 f184
    pct_chg               NUMERIC(8,2)   NOT NULL DEFAULT 0,  -- 涨跌幅 f3
    volume                BIGINT         NOT NULL DEFAULT 0,  -- 成交量（手）f5
    created_at            TIMESTAMPTZ    NOT NULL DEFAULT NOW()
);

-- ── 3. 异动告警表 ─────────────────────────────────────────────
-- DiscoveryService 检测到脉冲后写入
CREATE TABLE IF NOT EXISTS alerts (
    id              BIGSERIAL     PRIMARY KEY,
    stock_code      VARCHAR(10)   NOT NULL,
    stock_name      VARCHAR(50),
    alert_type      VARCHAR(30)   NOT NULL DEFAULT 'MONEY_FLOW_PULSE',
    -- 触发时的关键指标快照
    main_net_inflow NUMERIC(15,2) NOT NULL DEFAULT 0,
    delta           NUMERIC(15,2) NOT NULL DEFAULT 0,   -- 本次增量
    pct_chg         NUMERIC(8,2)  NOT NULL DEFAULT 0,
    message         TEXT,                                -- 人类可读的描述
    is_read         BOOLEAN       NOT NULL DEFAULT FALSE,
    triggered_at    TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    created_at      TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

-- ── 索引 ──────────────────────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_money_flow_code_date
    ON money_flow_logs (stock_code, date DESC);

CREATE INDEX IF NOT EXISTS idx_money_flow_date
    ON money_flow_logs (date DESC);

CREATE INDEX IF NOT EXISTS idx_alerts_triggered
    ON alerts (triggered_at DESC);

CREATE INDEX IF NOT EXISTS idx_alerts_code
    ON alerts (stock_code, triggered_at DESC);

CREATE INDEX IF NOT EXISTS idx_alerts_unread
    ON alerts (is_read, triggered_at DESC);

-- ── 验证 ──────────────────────────────────────────────────────
SELECT tablename FROM pg_tables
WHERE schemaname = 'public'
  AND tablename IN ('money_flow_logs', 'alerts')
ORDER BY tablename;
