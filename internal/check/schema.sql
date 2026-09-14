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

CREATE TABLE IF NOT EXISTS users (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  email         text        NOT NULL UNIQUE,   -- 應用層一律轉小寫，不裝 citext
  password_hash text        NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now()
);

-- 可為 NULL：M1/M2 期間建的 demo check 沒有擁有者，加 NOT NULL 會讓
-- migration 在既有資料上炸掉。孤兒 check 在所有列表（都帶 WHERE user_id）
-- 裡看不到，自然消失，不用寫清理腳本。
ALTER TABLE checks ADD COLUMN IF NOT EXISTS
  user_id uuid REFERENCES users(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS checks_user ON checks (user_id);

CREATE TABLE IF NOT EXISTS pings (
  id          bigserial PRIMARY KEY,   -- 只有時序讀取，不需要不可預測的主鍵
  check_id    uuid        NOT NULL REFERENCES checks(id) ON DELETE CASCADE,
  received_at timestamptz NOT NULL DEFAULT now(),
  remote_addr text,
  user_agent  text
);
CREATE INDEX IF NOT EXISTS pings_check ON pings (check_id, received_at DESC);
