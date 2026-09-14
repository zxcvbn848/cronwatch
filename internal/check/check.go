// Package check 是 cronwatch 的核心：ping 進來更新心跳，Sweep 找出逾期的 check。
//
// 授權邊界就在這個 package：除了 Ping，所有讀寫都必須帶 userID。
// 刻意不提供「只靠 id 取 check」的方法 —— 有那個方法，總有一天某個 handler 會用它。
// Ping 是唯一例外，因為 uuid 本身就是憑證。
package check

import (
	"context"
	_ "embed"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schema string

// ponytail: 用 CREATE TABLE IF NOT EXISTS 當 migration，等 schema 需要
// 不可冪等表達的變更（改欄位型別、改名）時再換 golang-migrate。
type Store struct{ db *pgxpool.Pool }

func New(db *pgxpool.Pool) *Store { return &Store{db: db} }

// Migrate 執行內嵌的 schema，冪等，每次啟動都跑。
func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.db.Exec(ctx, schema)
	return err
}

// Check 是一個監控項目。
type Check struct {
	ID         string     `db:"id"`
	Name       string     `db:"name"`
	PeriodSecs int        `db:"period_secs"`
	GraceSecs  int        `db:"grace_secs"`
	Status     string     `db:"status"`
	LastPingAt *time.Time `db:"last_ping_at"`
	NextDueAt  *time.Time `db:"next_due_at"`
	CreatedAt  time.Time  `db:"created_at"`
}

// Heartbeat 是一筆心跳紀錄。
type Heartbeat struct {
	ReceivedAt time.Time `db:"received_at"`
	RemoteAddr string    `db:"remote_addr"`
	UserAgent  string    `db:"user_agent"`
}

// Overdue 是一個已被標記為 down 的逾期 check。
type Overdue struct {
	ID        string
	Name      string
	NextDueAt time.Time
}

const cols = `id, name, period_secs, grace_secs, status, last_ping_at, next_due_at, created_at`

// invalidUUID 判斷錯誤是不是「這個字串不是 uuid」。
// id 來自 URL，使用者可以隨便亂打，那是 404 不是 500。
func invalidUUID(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "22P02"
}

// Create 建立 check，回傳的 id 同時是 ping URL 的路徑。
// userID 傳空字串則不屬於任何人 —— 只有還沒接上認證的臨時端點會這樣用。
func (s *Store) Create(ctx context.Context, userID, name string, period, grace int) (string, error) {
	var id string
	err := s.db.QueryRow(ctx, `
		INSERT INTO checks (user_id, name, period_secs, grace_secs)
		VALUES (NULLIF($1, '')::uuid, $2, $3, $4)
		RETURNING id`, userID, name, period, grace).Scan(&id)
	return id, err
}

// ListByUser 回傳這個使用者的所有 check，新的在前。
func (s *Store) ListByUser(ctx context.Context, userID string) ([]Check, error) {
	rows, err := s.db.Query(ctx,
		`SELECT `+cols+` FROM checks WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByName[Check])
}

// GetForUser 取單一 check。不是這個使用者的就回 found=false ——
// 不區分「不存在」與「存在但不是你的」，避免洩漏他人 id 的存在。
func (s *Store) GetForUser(ctx context.Context, id, userID string) (Check, bool, error) {
	rows, err := s.db.Query(ctx,
		`SELECT `+cols+` FROM checks WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		if invalidUUID(err) {
			return Check{}, false, nil
		}
		return Check{}, false, err
	}
	c, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Check])
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Check{}, false, nil
	case err != nil && invalidUUID(err):
		return Check{}, false, nil
	case err != nil:
		return Check{}, false, err
	}
	return c, true, nil
}

// UpdateForUser 改名稱與週期。
//
// 週期變了就要重算 next_due_at，不然編輯完要等到下一次 ping 才生效 ——
// 把週期從 24h 改成 1h 的人，期待的是現在就生效。
func (s *Store) UpdateForUser(ctx context.Context, id, userID, name string, period, grace int) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE checks
		   SET name        = $3,
		       period_secs = $4,
		       grace_secs  = $5,
		       next_due_at = CASE
		         WHEN last_ping_at IS NULL THEN next_due_at
		         ELSE last_ping_at + make_interval(secs => $4::int + $5::int)
		       END
		 WHERE id = $1 AND user_id = $2`, id, userID, name, period, grace)
	if err != nil {
		if invalidUUID(err) {
			return false, nil
		}
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// PauseForUser 暫停。paused 不在 Sweep 的 WHERE status='up' 裡，所以立刻安靜。
func (s *Store) PauseForUser(ctx context.Context, id, userID string) (bool, error) {
	return s.exec(ctx, `UPDATE checks SET status = 'paused' WHERE id = $1 AND user_id = $2`, id, userID)
}

// ResumeForUser 恢復監控。期限從現在起算，不然剛恢復就立刻逾期。
func (s *Store) ResumeForUser(ctx context.Context, id, userID string) (bool, error) {
	return s.exec(ctx, `
		UPDATE checks
		   SET status      = 'up',
		       next_due_at = now() + make_interval(secs => period_secs + grace_secs)
		 WHERE id = $1 AND user_id = $2 AND status = 'paused'`, id, userID)
}

// DeleteForUser 刪除。pings 靠 ON DELETE CASCADE 一起走。
func (s *Store) DeleteForUser(ctx context.Context, id, userID string) (bool, error) {
	return s.exec(ctx, `DELETE FROM checks WHERE id = $1 AND user_id = $2`, id, userID)
}

func (s *Store) exec(ctx context.Context, sql, id, userID string) (bool, error) {
	tag, err := s.db.Exec(ctx, sql, id, userID)
	if err != nil {
		if invalidUUID(err) {
			return false, nil
		}
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// RecentPings 回傳最近的心跳紀錄。join checks 是為了驗擁有者 ——
// 不能只用 check_id 查，那樣任何人都能看別人的心跳歷史。
func (s *Store) RecentPings(ctx context.Context, checkID, userID string, limit int) ([]Heartbeat, error) {
	rows, err := s.db.Query(ctx, `
		SELECT p.received_at,
		       COALESCE(p.remote_addr, '') AS remote_addr,
		       COALESCE(p.user_agent, '')  AS user_agent
		  FROM pings p JOIN checks c ON c.id = p.check_id
		 WHERE p.check_id = $1 AND c.user_id = $2
		 ORDER BY p.received_at DESC
		 LIMIT $3`, checkID, userID, limit)
	if err != nil {
		if invalidUUID(err) {
			return nil, nil
		}
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByName[Heartbeat])
}

// Ping 記錄一次心跳。一次 DB 來回同時完成三件事：
//
//  1. 更新 checks 的心跳與下次期限
//  2. 取得更新「之前」的 status（自我 join 一份 old，RETURNING 只給得到新值）
//  3. 把這次心跳寫進 pings
//
// 用 CTE 而不是非同步寫入：一樣只有一次來回，但不會掉紀錄。
// Postgres 保證 WITH 裡的寫入語句一定執行，即使主查詢沒有引用它。
//
// paused 的 check 被 ping 到會被叫醒（status 無條件設成 'up'）。
// 暫停的語意是「我知道它關著，別吵我」，心跳回來就表示它又開著了。
//
// found 為 false 表示 id 不存在（或根本不是 uuid）。
func (s *Store) Ping(ctx context.Context, id, remoteAddr, userAgent string) (found, recovered bool, name string, err error) {
	var prev string
	err = s.db.QueryRow(ctx, `
		WITH upd AS (
		  UPDATE checks c
		     SET last_ping_at = now(),
		         next_due_at  = now() + make_interval(secs => c.period_secs + c.grace_secs),
		         status       = 'up'
		    FROM checks old
		   WHERE c.id = old.id AND c.id = $1
		  RETURNING c.id, c.name, old.status AS prev
		), ins AS (
		  INSERT INTO pings (check_id, remote_addr, user_agent)
		  SELECT id, $2, $3 FROM upd
		)
		SELECT name, prev FROM upd`, id, remoteAddr, userAgent).Scan(&name, &prev)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, false, "", nil
	case err != nil && invalidUUID(err):
		return false, false, "", nil
	case err != nil:
		return false, false, "", err
	}
	return true, prev == "down", name, nil
}

// Sweep 把逾期的 check 標成 down，並回傳這次剛轉換的那些。
//
// 一句 UPDATE ... RETURNING 同時完成「找出」與「標記」，所以天生冪等：
// 已經是 down 的不會再被回傳。「通知只在狀態轉換時發」直接靠這個性質。
func (s *Store) Sweep(ctx context.Context) ([]Overdue, error) {
	rows, err := s.db.Query(ctx, `
		UPDATE checks SET status = 'down'
		 WHERE status = 'up' AND next_due_at <= now()
		 RETURNING id, name, next_due_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Overdue
	for rows.Next() {
		var o Overdue
		if err := rows.Scan(&o.ID, &o.Name, &o.NextDueAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
