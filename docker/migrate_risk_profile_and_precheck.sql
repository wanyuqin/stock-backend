CREATE TABLE IF NOT EXISTS user_risk_profiles (
    id                  BIGSERIAL PRIMARY KEY,
    user_id             BIGINT       NOT NULL UNIQUE DEFAULT 1,
    risk_per_trade_pct  NUMERIC(5,2) NOT NULL DEFAULT 1.00,
    max_position_pct    NUMERIC(5,2) NOT NULL DEFAULT 15.00,
    account_size        NUMERIC(15,2) NOT NULL DEFAULT 200000.00,
    created_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS trade_precheck_logs (
    id                BIGSERIAL PRIMARY KEY,
    user_id           BIGINT        NOT NULL DEFAULT 1,
    stock_code        VARCHAR(10)   NOT NULL,
    buy_price         NUMERIC(12,4) NOT NULL,
    stop_loss_price   NUMERIC(12,4) NOT NULL,
    target_price      NUMERIC(12,4),
    planned_amount    NUMERIC(15,2) NOT NULL,
    reason            TEXT          NOT NULL DEFAULT '',
    estimated_volume  BIGINT        NOT NULL DEFAULT 0,
    worst_loss_amount NUMERIC(15,2) NOT NULL DEFAULT 0,
    worst_loss_pct    NUMERIC(8,2)  NOT NULL DEFAULT 0,
    pass              BOOLEAN       NOT NULL DEFAULT FALSE,
    fail_reason       TEXT          NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_trade_precheck_logs_user_created ON trade_precheck_logs (user_id, created_at DESC);
