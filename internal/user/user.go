// Package user 是帳號：註冊與密碼驗證。
package user

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrDuplicate    = errors.New("這個 email 已經註冊過了")
	ErrInvalidEmail = errors.New("email 格式看起來不對")
	ErrWeakPassword = errors.New("密碼至少 8 個字元")
)

type Store struct{ db *pgxpool.Pool }

func New(db *pgxpool.Pool) *Store { return &Store{db: db} }

// normalize 讓 email 大小寫不敏感。在應用層做，省一個 citext extension。
func normalize(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// Create 註冊。回傳新使用者的 id。
func (s *Store) Create(ctx context.Context, email, password string) (string, error) {
	email = normalize(email)
	// 信任邊界上的驗證不省
	if !strings.Contains(email, "@") || len(email) < 3 {
		return "", ErrInvalidEmail
	}
	if len(password) < 8 {
		return "", ErrWeakPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	var id string
	err = s.db.QueryRow(ctx,
		`INSERT INTO users (email, password_hash) VALUES ($1, $2) RETURNING id`,
		email, string(hash)).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return "", ErrDuplicate // unique 違反是使用者錯誤，不是 500
	}
	return id, err
}

// dummyHash 讓「帳號不存在」也要花掉一次 bcrypt 的時間，
// 否則回應快慢會洩漏哪些 email 有註冊過。
var dummyHash = sync.OnceValue(func() []byte {
	h, _ := bcrypt.GenerateFromPassword([]byte("dummy"), bcrypt.DefaultCost)
	return h
})

// Authenticate 驗證密碼。ok 為 false 時不區分「帳號不存在」與「密碼錯誤」——
// 區分了就等於提供帳號列舉的管道。
func (s *Store) Authenticate(ctx context.Context, email, password string) (string, bool, error) {
	var id, hash string
	err := s.db.QueryRow(ctx,
		`SELECT id, password_hash FROM users WHERE email = $1`, normalize(email)).Scan(&id, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		bcrypt.CompareHashAndPassword(dummyHash(), []byte(password))
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return "", false, nil
	}
	return id, true, nil
}

// Email 用 id 取 email，給畫面顯示「已登入為 …」與通知收件人用。
func (s *Store) Email(ctx context.Context, id string) (string, error) {
	var email string
	err := s.db.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, id).Scan(&email)
	return email, err
}
