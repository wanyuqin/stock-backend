-- 如果 trade_logs 表已存在（旧容器），执行此脚本补充 reason 列
-- 使用方法：
--   docker exec -i <postgres容器名> psql -U admin -d stock_system < migrate_add_reason.sql

ALTER TABLE trade_logs
    ADD COLUMN IF NOT EXISTS reason TEXT DEFAULT '';

-- 同时补充 traded_at 索引（如果之前没有）
CREATE INDEX IF NOT EXISTS idx_trade_logs_traded ON trade_logs (traded_at DESC);
