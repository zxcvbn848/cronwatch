package user

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func open(t *testing.T) (*Store, context.Context) {
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
	return New(db), ctx
}

func TestRegisterAndAuthenticate(t *testing.T) {
	s, ctx := open(t)
	email := "T-" + time.Now().Format("20060102150405.000000000") + "@Example.com"
	const password = "correct horse"

	id, err := s.Create(ctx, email, password)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.db.Exec(ctx, `DELETE FROM users WHERE id = $1`, id) })

	// 對的密碼過
	got, ok, err := s.Authenticate(ctx, email, password)
	if err != nil || !ok || got != id {
		t.Fatalf("Authenticate = %q, %v, %v；預期 %q, true, nil", got, ok, err, id)
	}

	// email 大小寫不敏感
	if _, ok, _ := s.Authenticate(ctx, "t-"+email[2:], password); !ok {
		t.Error("email 大小寫不同就登不進去")
	}

	// 錯的密碼不過，而且不能回 error（那會變成「帳號存在」的訊號）
	if _, ok, err := s.Authenticate(ctx, email, "wrong password"); ok || err != nil {
		t.Errorf("錯密碼 = %v, %v；預期 false, nil", ok, err)
	}

	// 不存在的帳號同樣是 false, nil，與錯密碼無法區分
	if _, ok, err := s.Authenticate(ctx, "nobody@example.com", password); ok || err != nil {
		t.Errorf("不存在的帳號 = %v, %v；預期 false, nil", ok, err)
	}

	// 重複 email 要被擋，而且是使用者錯誤不是 500
	if _, err := s.Create(ctx, email, password); !errors.Is(err, ErrDuplicate) {
		t.Errorf("重複註冊 err = %v；預期 ErrDuplicate", err)
	}
}

func TestCreateValidates(t *testing.T) {
	s, ctx := open(t)
	if _, err := s.Create(ctx, "not-an-email", "long enough"); !errors.Is(err, ErrInvalidEmail) {
		t.Errorf("err = %v；預期 ErrInvalidEmail", err)
	}
	if _, err := s.Create(ctx, "a@b.com", "short"); !errors.Is(err, ErrWeakPassword) {
		t.Errorf("err = %v；預期 ErrWeakPassword", err)
	}
}
