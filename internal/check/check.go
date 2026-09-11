// Package check 是 cronwatch 的核心：ping 進來更新心跳，Sweep 找出逾期的 check。
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

// Overdue 是一個已被標記為 down 的逾期 check。
type Overdue struct {
	ID        string
	Name      string
	NextDueAt time.Time
}

// Create 建立 check，回傳的 id 同時是 ping URL 的路徑。
func (s *Store) Create(ctx context.Context, name string, period, grace int) (string, error) {
	var id string
	err := s.db.QueryRow(ctx,
		`INSERT INTO checks (name, period_secs, grace_secs) VALUES ($1, $2, $3) RETURNING id`,
		name, period, grace).Scan(&id)
	return id, err
}

// Ping 記錄一次心跳。一句 UPDATE，不查詢、不 join —— 這是流量最高的路徑。
//
// 自我 join 一份 old 是為了拿到更新「之前」的 status：down → up 就是恢復，
// 要發恢復通知。RETURNING 只給得到新值，所以得這樣取舊值。
// 仍然是單句 SQL、兩次主鍵查找。
//
// found 為 false 表示 id 不存在（或根本不是 uuid）。
func (s *Store) Ping(ctx context.Context, id string) (found, recovered bool, name string, err error) {
	var prev string
	err = s.db.QueryRow(ctx, `
		UPDATE checks c
		   SET last_ping_at = now(),
		       next_due_at  = now() + make_interval(secs => c.period_secs + c.grace_secs),
		       status       = 'up'
		  FROM checks old
		 WHERE c.id = old.id AND c.id = $1
		RETURNING old.status, c.name`, id).Scan(&prev, &name)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, false, "", nil
	case err != nil:
		// id 來自使用者輸入，不是 uuid 時 Postgres 回 22P02。這是 404 不是 500。
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "22P02" {
			return false, false, "", nil
		}
		return false, false, "", err
	}
	return true, prev == "down", name, nil
}

// Sweep 把逾期的 check 標成 down，並回傳這次剛轉換的那些。
//
// 一句 UPDATE ... RETURNING 同時完成「找出」與「標記」，所以天生冪等：
// 已經是 down 的不會再被回傳。M2 的「只在狀態轉換時發通知」直接靠這個性質。
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
