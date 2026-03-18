-- migrate_screener_templates.sql
-- 用户自定义筛选模板

CREATE TABLE IF NOT EXISTS screener_templates (
    id           BIGSERIAL PRIMARY KEY,
    user_id      BIGINT        NOT NULL DEFAULT 1,
    name         VARCHAR(100)  NOT NULL,
    description  TEXT          NOT NULL DEFAULT '',
    params       JSONB         NOT NULL DEFAULT '{}',
    push_enabled BOOLEAN       NOT NULL DEFAULT FALSE,
    created_at   TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_screener_templates_user ON screener_templates (user_id);
