-- docker/migrate_position_enhancements.sql
-- 持仓守护增强：持仓天数、买入理由、关联买入计划

ALTER TABLE position_details ADD COLUMN IF NOT EXISTS linked_plan_id     BIGINT;
ALTER TABLE position_details ADD COLUMN IF NOT EXISTS plan_stop_loss     NUMERIC(15,4);
ALTER TABLE position_details ADD COLUMN IF NOT EXISTS plan_target_price  NUMERIC(15,4);
ALTER TABLE position_details ADD COLUMN IF NOT EXISTS plan_buy_reason    TEXT NOT NULL DEFAULT '';
ALTER TABLE position_details ADD COLUMN IF NOT EXISTS bought_at          TIMESTAMPTZ;
ALTER TABLE position_details ADD COLUMN IF NOT EXISTS buy_reason         TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_position_details_linked_plan ON position_details (linked_plan_id);
