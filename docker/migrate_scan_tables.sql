-- ================================================================
-- 迁移脚本：新增 daily_scans 和 daily_reports 两张表
-- 在已运行的 PostgreSQL 中执行（幂等，IF NOT EXISTS）
-- 执行方式：
--   docker exec -i <容器名> psql -U admin -d stock_system < migrate_scan_tables.sql
-- ================================================================

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

CREATE TABLE IF NOT EXISTS daily_reports (
    id           BIGSERIAL    PRIMARY KEY,
    report_date  DATE         NOT NULL UNIQUE DEFAULT CURRENT_DATE,
    content      TEXT         NOT NULL,
    market_mood  VARCHAR(20),
    scan_count   INT,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_scans_date       ON daily_scans   (scan_date);
CREATE INDEX IF NOT EXISTS idx_scans_code_date  ON daily_scans   (stock_code, scan_date);
CREATE INDEX IF NOT EXISTS idx_reports_date     ON daily_reports  (report_date);

-- 验证
SELECT tablename FROM pg_tables
WHERE schemaname = 'public'
  AND tablename IN ('daily_scans', 'daily_reports')
ORDER BY tablename;
