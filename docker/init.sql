-- =============================================================
-- 个人 A 股分析系统 - 数据库初始化脚本
-- 对应 docker-compose 中的 stock_system 数据库
-- =============================================================

-- ── 股票基础信息表 ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS stocks (
    id         BIGSERIAL    PRIMARY KEY,
    code       VARCHAR(10)  NOT NULL UNIQUE,
    name       VARCHAR(50)  NOT NULL,
    market     VARCHAR(4)   NOT NULL,
    sector     VARCHAR(50)  DEFAULT '',
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- ── 自选股表 ──────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS watchlist (
    id         BIGSERIAL    PRIMARY KEY,
    user_id    BIGINT       NOT NULL DEFAULT 1,
    stock_code VARCHAR(10)  NOT NULL,
    note       TEXT         DEFAULT '',
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, stock_code)
);

-- ── 交易日志表 ────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS trade_logs (
    id         BIGSERIAL     PRIMARY KEY,
    user_id    BIGINT        NOT NULL DEFAULT 1,
    stock_code VARCHAR(10)   NOT NULL,
    action     VARCHAR(4)    NOT NULL CHECK (action IN ('BUY', 'SELL')),
    price      NUMERIC(12,4) NOT NULL,
    volume     BIGINT        NOT NULL,
    traded_at  TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    reason     TEXT          DEFAULT '',
    created_at TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

-- ── AI 分析缓存表 ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS ai_cache (
    id         BIGSERIAL    PRIMARY KEY,
    stock_code VARCHAR(10)  NOT NULL,
    prompt     TEXT         NOT NULL,
    response   TEXT         NOT NULL,
    model_used VARCHAR(50)  DEFAULT '',
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ  NOT NULL DEFAULT NOW() + INTERVAL '24 hours'
);

-- ── 自动化扫描结果表 ──────────────────────────────────────────
-- signals: JSONB 数组，如 ["VOLUME_UP", "MA20_BREAK"]，单股可触发多个信号
CREATE TABLE IF NOT EXISTS daily_scans (
    id           BIGSERIAL     PRIMARY KEY,
    scan_date    DATE          NOT NULL DEFAULT CURRENT_DATE,
    stock_code   VARCHAR(10)   NOT NULL,
    stock_name   VARCHAR(50),
    signals      JSONB         NOT NULL,
    price        NUMERIC(12,4),
    pct_chg      NUMERIC(8,2),
    volume_ratio NUMERIC(8,2),
    ma_status    VARCHAR(50),
    created_at   TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

-- ── 自动化报告表（存储 AI 总结） ────────────────────────────────
CREATE TABLE IF NOT EXISTS daily_reports (
    id           BIGSERIAL    PRIMARY KEY,
    report_date  DATE         NOT NULL UNIQUE DEFAULT CURRENT_DATE,
    content      TEXT         NOT NULL,
    market_mood  VARCHAR(20),
    scan_count   INT,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- ── 索引 ──────────────────────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_watchlist_user    ON watchlist    (user_id);
CREATE INDEX IF NOT EXISTS idx_trade_logs_user   ON trade_logs   (user_id);
CREATE INDEX IF NOT EXISTS idx_trade_logs_code   ON trade_logs   (stock_code);
CREATE INDEX IF NOT EXISTS idx_trade_logs_traded ON trade_logs   (traded_at DESC);
CREATE INDEX IF NOT EXISTS idx_ai_cache_code     ON ai_cache     (stock_code);
CREATE INDEX IF NOT EXISTS idx_ai_cache_expires  ON ai_cache     (expires_at);
CREATE INDEX IF NOT EXISTS idx_scans_date        ON daily_scans  (scan_date);
CREATE INDEX IF NOT EXISTS idx_scans_code_date   ON daily_scans  (stock_code, scan_date);
CREATE INDEX IF NOT EXISTS idx_reports_date      ON daily_reports (report_date);

-- ── 示例数据 ──────────────────────────────────────────────────
INSERT INTO stocks (code, name, market, sector) VALUES
    ('600519', '贵州茅台', 'SH', '食品饮料'),
    ('000858', '五粮液',   'SZ', '食品饮料'),
    ('601318', '中国平安', 'SH', '金融'),
    ('000001', '平安银行', 'SZ', '金融'),
    ('600036', '招商银行', 'SH', '金融')
ON CONFLICT (code) DO NOTHING;
