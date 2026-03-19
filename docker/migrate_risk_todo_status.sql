CREATE TABLE IF NOT EXISTS risk_todo_statuses (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT NOT NULL DEFAULT 1,
  todo_date DATE NOT NULL,
  todo_id VARCHAR(120) NOT NULL,
  done BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_risk_todo_user_date_id
  ON risk_todo_statuses (user_id, todo_date, todo_id);

CREATE INDEX IF NOT EXISTS idx_risk_todo_user_date
  ON risk_todo_statuses (user_id, todo_date);
