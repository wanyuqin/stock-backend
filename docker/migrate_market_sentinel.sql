
-- 市场情绪/宏观监控表
CREATE TABLE IF NOT EXISTS market_sentiment (
    id                BIGSERIAL    PRIMARY KEY,
    trade_date        DATE         UNIQUE NOT NULL,
    total_amount      NUMERIC(18,2), -- 两市成交额
    up_count          INT,           -- 上涨家数
    down_count        INT,           -- 下跌家数
    limit_up_count    INT,           -- 涨停家数
    limit_down_count  INT,           -- 跌停家数
    sentiment_score   INT,           -- 综合热度(0-100)
    created_at        TIMESTAMPTZ  DEFAULT NOW()
);

-- 索引：按交易日查询
CREATE INDEX IF NOT EXISTS idx_market_sentiment_date ON market_sentiment(trade_date);
