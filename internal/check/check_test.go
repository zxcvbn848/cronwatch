package check

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// 需要真的 Postgres：
//
//	TEST_DATABASE_URL=postgres://cronwatch:cronwatch@localhost:5432/cronwatch go test ./...
//
// 邏輯全在 SQL 裡，mock 掉 DB 的測試等於什麼都沒測。
func open(t *testing.T) (*Store, *pgxpool.Pool, context.Context) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("未設定 TEST_DATABASE_URL")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	s := New(db)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s, db, ctx
}

// newUser 直接寫 users 表 —— 這個 package 不負責認證。
func newUser(t *testing.T, db *pgxpool.Pool, ctx context.Context) string {
	t.Helper()
	var id string
	email := "t-" + time.Now().Format("20060102150405.000000000") + "@example.com"
	if err := db.QueryRow(ctx,
		`INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id`,
		email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(ctx, `DELETE FROM users WHERE id = $1`, id) })
	return id
}

func TestPingAndSweep(t *testing.T) {
	s, db, ctx := open(t)
	user := newUser(t, db, ctx)

	id, err := s.Create(ctx, user, "nightly", 60, 0)
	if err != nil {
		t.Fatal(err)
	}

	// ping → up，且下次期限在未來。第一次 ping 是 new → up，不算恢復
	found, recovered, name, err := s.Ping(ctx, id, "10.0.0.1", "curl/8")
	if err != nil || !found {
		t.Fatalf("Ping = %v, %v；預期 true, nil", found, err)
	}
	if recovered {
		t.Error("new → up 不該算恢復，會多寄一封信")
	}
	if name != "nightly" {
		t.Errorf("name = %q，預期 nightly", name)
	}

	c, ok, err := s.GetForUser(ctx, id, user)
	if err != nil || !ok {
		t.Fatalf("GetForUser = %v, %v", ok, err)
	}
	if c.Status != "up" {
		t.Errorf("status = %q，預期 up", c.Status)
	}
	if c.NextDueAt == nil || !c.NextDueAt.After(time.Now()) {
		t.Errorf("next_due_at = %v，預期在未來", c.NextDueAt)
	}

	// 心跳要寫進 pings，詳情頁的歷史靠這個
	hb, err := s.RecentPings(ctx, id, user, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hb) != 1 {
		t.Fatalf("心跳歷史 %d 筆，預期 1 筆", len(hb))
	}
	if hb[0].RemoteAddr != "10.0.0.1" || hb[0].UserAgent != "curl/8" {
		t.Errorf("心跳紀錄 = %+v，remote_addr / user_agent 沒寫進去", hb[0])
	}

	// 還沒到期 → Sweep 不該動它
	if od, err := s.Sweep(ctx); err != nil {
		t.Fatal(err)
	} else if containsID(od, id) {
		t.Error("未到期就被標成 down")
	}

	// 把期限推到過去 → Sweep 應該抓到並標成 down
	if _, err := db.Exec(ctx,
		`UPDATE checks SET next_due_at = now() - interval '1 second' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	od, err := s.Sweep(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !containsID(od, id) {
		t.Fatalf("逾期的 check 沒被 Sweep 抓到：%+v", od)
	}

	// 冪等：第二次 Sweep 不該再回傳它（通知不重複寄靠這個）
	if od, err := s.Sweep(ctx); err != nil {
		t.Fatal(err)
	} else if containsID(od, id) {
		t.Error("同一個 check 被 Sweep 回傳兩次，通知會重複寄")
	}

	// down → up 是恢復，要發恢復通知
	if _, recovered, _, err = s.Ping(ctx, id, "", ""); err != nil {
		t.Fatal(err)
	} else if !recovered {
		t.Error("down → up 沒被認定為恢復，恢復通知不會發出")
	}

	// up → up 不是恢復，不然每次 ping 都寄一封
	if _, recovered, _, err = s.Ping(ctx, id, "", ""); err != nil {
		t.Fatal(err)
	} else if recovered {
		t.Error("up → up 被誤判成恢復，每次心跳都會寄信")
	}

	// 不是 uuid 的 id 是 404，不是錯誤
	if found, _, _, err := s.Ping(ctx, "not-a-uuid", "", ""); err != nil || found {
		t.Errorf("Ping(\"not-a-uuid\") = %v, %v；預期 false, nil", found, err)
	}
}

// TestUserScoping 是這個 package 最重要的測試：
// 這條壞了就是資料外洩，別人的 check 會被看到或被改掉。
func TestUserScoping(t *testing.T) {
	s, db, ctx := open(t)
	alice, bob := newUser(t, db, ctx), newUser(t, db, ctx)

	id, err := s.Create(ctx, alice, "alice 的 check", 60, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.Ping(ctx, id, "", ""); err != nil {
		t.Fatal(err)
	}

	// bob 知道 id 也拿不到
	if _, ok, err := s.GetForUser(ctx, id, bob); err != nil || ok {
		t.Errorf("bob 拿到了 alice 的 check：ok=%v err=%v", ok, err)
	}
	if list, err := s.ListByUser(ctx, bob); err != nil || len(list) != 0 {
		t.Errorf("bob 的列表有 %d 筆，預期 0 筆", len(list))
	}
	if hb, err := s.RecentPings(ctx, id, bob, 10); err != nil || len(hb) != 0 {
		t.Errorf("bob 看到 %d 筆 alice 的心跳歷史，預期 0 筆", len(hb))
	}

	// bob 也改不動、暫停不了、刪不掉
	for _, tc := range []struct {
		name string
		fn   func() (bool, error)
	}{
		{"UpdateForUser", func() (bool, error) { return s.UpdateForUser(ctx, id, bob, "被改了", 1, 1) }},
		{"TogglePauseForUser", func() (bool, error) { return s.TogglePauseForUser(ctx, id, bob) }},
		{"DeleteForUser", func() (bool, error) { return s.DeleteForUser(ctx, id, bob) }},
	} {
		if ok, err := tc.fn(); err != nil || ok {
			t.Errorf("bob 對 alice 的 check 執行 %s 成功了：ok=%v err=%v", tc.name, ok, err)
		}
	}

	// alice 自己還在，而且沒被 bob 改掉
	c, ok, err := s.GetForUser(ctx, id, alice)
	if err != nil || !ok {
		t.Fatalf("alice 的 check 不見了：ok=%v err=%v", ok, err)
	}
	if c.Name != "alice 的 check" {
		t.Errorf("name = %q，被 bob 改掉了", c.Name)
	}
}

// TestEditRecomputesDue：改週期要立刻生效，不能等下一次 ping。
func TestEditRecomputesDue(t *testing.T) {
	s, db, ctx := open(t)
	user := newUser(t, db, ctx)

	id, err := s.Create(ctx, user, "報表", 86400, 3600)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.Ping(ctx, id, "", ""); err != nil {
		t.Fatal(err)
	}
	before, _, _ := s.GetForUser(ctx, id, user)

	if ok, err := s.UpdateForUser(ctx, id, user, "報表", 60, 0); err != nil || !ok {
		t.Fatalf("UpdateForUser = %v, %v", ok, err)
	}
	after, _, _ := s.GetForUser(ctx, id, user)

	if !after.NextDueAt.Before(*before.NextDueAt) {
		t.Errorf("週期從 24h 改成 1m，next_due_at 卻沒提前：%v → %v",
			before.NextDueAt, after.NextDueAt)
	}
}

func containsID(od []Overdue, id string) bool {
	for _, o := range od {
		if o.ID == id {
			return true
		}
	}
	return false
}
