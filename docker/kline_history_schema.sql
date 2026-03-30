-- ═══════════════════════════════════════════════════════════════
-- K 线历史数据表  stock_kline_daily
-- ═══════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS stock_kline_daily (
    code        VARCHAR(10)    NOT NULL,
    trade_date  DATE           NOT NULL,
    open        NUMERIC(10,3)  NOT NULL,
    close       NUMERIC(10,3)  NOT NULL,
    high        NUMERIC(10,3)  NOT NULL,
    low         NUMERIC(10,3)  NOT NULL,
    volume      BIGINT         NOT NULL DEFAULT 0,
    amount      NUMERIC(16,2)  NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    PRIMARY KEY (code, trade_date)
);

-- ═══════════════════════════════════════════════════════════════
-- K 线同步状态表  stock_kline_sync_status
-- ═══════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS stock_kline_sync_status (
    code            VARCHAR(10)  NOT NULL PRIMARY KEY,
    stock_name      VARCHAR(50)  NOT NULL DEFAULT '',
    earliest_date   DATE,
    latest_date     DATE,
    total_bars      INT          NOT NULL DEFAULT 0,
    sync_state      VARCHAR(20)  NOT NULL DEFAULT 'idle',
    last_error      TEXT,
    last_synced_at  TIMESTAMPTZ,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
