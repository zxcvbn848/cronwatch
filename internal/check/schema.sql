CREATE TABLE IF NOT EXISTS checks (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name         text        NOT NULL,
  period_secs  int         NOT NULL,
  grace_secs   int         NOT NULL DEFAULT 60,
  status       text        NOT NULL DEFAULT 'new',
  last_ping_at timestamptz,
  next_due_at  timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now()
);

-- 偵測迴圈唯一的查詢走這個索引，成本與逾期數量成正比，而非總 check 數
CREATE INDEX IF NOT EXISTS checks_due ON checks (next_due_at) WHERE status = 'up';
