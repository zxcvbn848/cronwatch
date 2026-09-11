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
func TestPingAndSweep(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("未設定 TEST_DATABASE_URL")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	s := New(db)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	id, err := s.Create(ctx, "test-"+time.Now().Format(time.RFC3339Nano), 60, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(ctx, `DELETE FROM checks WHERE id = $1`, id) })

	// ping → up，且下次期限在未來。第一次 ping 是 new → up，不算恢復
	found, recovered, name, err := s.Ping(ctx, id)
	if err != nil || !found {
		t.Fatalf("Ping = %v, %v；預期 true, nil", found, err)
	}
	if recovered {
		t.Error("new → up 不該算恢復，會多寄一封信")
	}
	if name == "" {
		t.Error("Ping 沒回傳 name，通知信會沒有標題")
	}
	var status string
	var due time.Time
	if err := db.QueryRow(ctx, `SELECT status, next_due_at FROM checks WHERE id = $1`, id).
		Scan(&status, &due); err != nil {
		t.Fatal(err)
	}
	if status != "up" {
		t.Errorf("status = %q，預期 up", status)
	}
	if !due.After(time.Now()) {
		t.Errorf("next_due_at = %v，預期在未來", due)
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
	if err := db.QueryRow(ctx, `SELECT status FROM checks WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "down" {
		t.Errorf("status = %q，預期 down", status)
	}

	// 冪等：第二次 Sweep 不該再回傳它（通知不重複寄靠這個）
	if od, err := s.Sweep(ctx); err != nil {
		t.Fatal(err)
	} else if containsID(od, id) {
		t.Error("同一個 check 被 Sweep 回傳兩次，通知會重複寄")
	}

	// down → up 是恢復，要發恢復通知
	_, recovered, _, err = s.Ping(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !recovered {
		t.Error("down → up 沒被認定為恢復，恢復通知不會發出")
	}

	// up → up 不是恢復，不然每次 ping 都寄一封
	if _, recovered, _, err = s.Ping(ctx, id); err != nil {
		t.Fatal(err)
	} else if recovered {
		t.Error("up → up 被誤判成恢復，每次心跳都會寄信")
	}

	// 不是 uuid 的 id 是 404，不是錯誤
	if found, _, _, err := s.Ping(ctx, "not-a-uuid"); err != nil || found {
		t.Errorf("Ping(\"not-a-uuid\") = %v, %v；預期 false, nil", found, err)
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
