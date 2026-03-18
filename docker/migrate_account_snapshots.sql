-- migrate_account_snapshots.sql
-- 账户净值快照表，每日盘后写入一次

CREATE TABLE IF NOT EXISTS account_snapshots (
    id              BIGSERIAL PRIMARY KEY,
    snapshot_date   DATE          NOT NULL,
    equity          NUMERIC(18,2) NOT NULL DEFAULT 0,
    realized_pnl    NUMERIC(18,2) NOT NULL DEFAULT 0,
    unrealized_pnl  NUMERIC(18,2) NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    CONSTRAINT account_snapshots_date_unique UNIQUE (snapshot_date)
);

CREATE INDEX IF NOT EXISTS idx_account_snapshots_date ON account_snapshots (snapshot_date DESC);
