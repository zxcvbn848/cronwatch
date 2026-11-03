package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	cookieName = "cw_session"
	sessionTTL = 30 * 24 * time.Hour
)

// Session 是 HMAC 簽名的 cookie。沒有伺服器端儲存，沒有依賴。
//
// cookie 值 = base64(userID|expiryUnix) . base64(hmac)
type Session struct{ secret []byte }

// NewSession 從 SESSION_SECRET 取密鑰。沒設就隨機產生 ——
// 自架單機這是可接受的預設，代價是重啟後所有人被登出。
func NewSession() *Session {
	if s := os.Getenv("SESSION_SECRET"); s != "" {
		return &Session{secret: []byte(s)}
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("產生 session 密鑰失敗: %v", err)
	}
	log.Print("未設定 SESSION_SECRET，已隨機產生 —— 重啟後所有人會被登出")
	return &Session{secret: b}
}

var enc = base64.RawURLEncoding

func (s *Session) mac(payload []byte) string {
	m := hmac.New(sha256.New, s.secret)
	m.Write(payload)
	return enc.EncodeToString(m.Sum(nil))
}

// Issue 簽發登入 cookie。
func (s *Session) Issue(c *gin.Context, userID string) {
	payload := []byte(userID + "|" + strconv.FormatInt(time.Now().Add(sessionTTL).Unix(), 10))
	value := enc.EncodeToString(payload) + "." + s.mac(payload)
	s.setCookie(c, value, int(sessionTTL.Seconds()))
}

// Clear 登出。
func (s *Session) Clear(c *gin.Context) { s.setCookie(c, "", -1) }

func (s *Session) setCookie(c *gin.Context, value string, maxAge int) {
	c.SetSameSite(http.SameSiteLaxMode)
	// 零設定判斷 https：本機 http 與 proxy 後面的 https 都對
	secure := c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https"
	c.SetCookie(cookieName, value, maxAge, "/", "", secure, true)
}

// userID 驗證 cookie 並取出使用者 id。
func (s *Session) userID(c *gin.Context) (string, bool) {
	raw, err := c.Cookie(cookieName)
	if err != nil {
		return "", false
	}
	encoded, sig, found := strings.Cut(raw, ".")
	if !found {
		return "", false
	}
	payload, err := enc.DecodeString(encoded)
	if err != nil {
		return "", false
	}
	if !hmac.Equal([]byte(sig), []byte(s.mac(payload))) {
		return "", false
	}
	id, exp, found := strings.Cut(string(payload), "|")
	if !found {
		return "", false
	}
	unix, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || time.Now().Unix() >= unix {
		return "", false
	}
	return id, true
}

// CSRFToken 是使用者專屬的固定 token，不需要伺服器端儲存。
func (s *Session) CSRFToken(userID string) string {
	return s.mac([]byte(userID + "|csrf"))
}

const ctxUserID = "userID"

// RequireAuth 擋下未登入的請求，並在 POST 時比對 CSRF token。
//
// 所有改變狀態的操作都是 POST，沒有任何 GET 會改資料 —— 這是 CSRF 防護成立的前提。
func (s *Session) RequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := s.userID(c)
		if !ok {
			c.Redirect(http.StatusSeeOther, "/login")
			c.Abort()
			return
		}
		if c.Request.Method == http.MethodPost &&
			!hmac.Equal([]byte(c.PostForm("csrf")), []byte(s.CSRFToken(id))) {
			c.String(http.StatusForbidden, "CSRF token 不符，請重新整理頁面再試一次")
			c.Abort()
			return
		}
		c.Set(ctxUserID, id)
		c.Next()
	}
}

// currentUser 只能在 RequireAuth 後面使用。
func currentUser(c *gin.Context) string { return c.GetString(ctxUserID) }
