-- migrate_buy_context.sql
-- 为 trade_reviews 表增加买入价格行为上下文分析字段
-- 存储为 JSONB，包含日内位置、MA偏离度、量能比、趋势状态等客观量化数据

ALTER TABLE trade_reviews
    ADD COLUMN IF NOT EXISTS buy_context JSONB DEFAULT NULL;

COMMENT ON COLUMN trade_reviews.buy_context IS
    '买入价格行为上下文（JSONB），基于K线客观计算，不依赖理由文字。
     包含：日内位置比例、MA20偏离度、量能比、趋势状态、前N日涨幅、行为标签等。';

-- 为 JSONB 列建部分索引，方便查询追高/低吸等特定行为
CREATE INDEX IF NOT EXISTS idx_reviews_buy_context_label
    ON trade_reviews USING GIN (buy_context)
    WHERE buy_context IS NOT NULL;
