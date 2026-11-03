package web

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// session 是純函式加 cookie，不需要 DB —— 最便宜的一條測試。
func newTestSession() *Session { return &Session{secret: []byte("test-secret")} }

// issue 簽發一個 cookie 並回傳它的值。
func issue(t *testing.T, s *Session, userID string) string {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	s.Issue(c, userID)

	cookie := w.Header().Get("Set-Cookie")
	if cookie == "" {
		t.Fatal("Issue 沒有設 cookie")
	}
	for _, want := range []string{"HttpOnly", "SameSite=Lax", "Path=/"} {
		if !strings.Contains(cookie, want) {
			t.Errorf("cookie 少了 %s：%s", want, cookie)
		}
	}
	value, _, _ := strings.Cut(strings.TrimPrefix(cookie, cookieName+"="), ";")
	return value
}

// readBack 把 cookie 值塞回請求，看 userID 認不認。
func readBack(s *Session, value string) (string, bool) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.AddCookie(&http.Cookie{Name: cookieName, Value: value})
	return s.userID(c)
}

func TestSessionRoundTrip(t *testing.T) {
	s := newTestSession()
	got, ok := readBack(s, issue(t, s, "user-1"))
	if !ok || got != "user-1" {
		t.Fatalf("userID = %q, %v；預期 user-1, true", got, ok)
	}
}

// 這條是重點：簽名擋不擋得住偽造。擋不住就等於任何人都能登入成任何人。
func TestSessionRejectsTampering(t *testing.T) {
	s := newTestSession()
	good := issue(t, s, "alice")
	payload, sig, _ := strings.Cut(good, ".")

	forged := enc.EncodeToString([]byte("bob|" + strconv.FormatInt(
		time.Now().Add(time.Hour).Unix(), 10)))

	for _, tc := range []struct{ name, value string }{
		{"改掉 payload 但沿用舊簽名", forged + "." + sig},
		{"改掉簽名", payload + "." + enc.EncodeToString([]byte("whatever"))},
		{"沒有簽名", payload},
		{"空字串", ""},
		{"別人的密鑰簽的", issue(t, &Session{secret: []byte("other")}, "alice")},
	} {
		if id, ok := readBack(s, tc.value); ok {
			t.Errorf("%s 竟然通過了，拿到 id=%q", tc.name, id)
		}
	}
}

func TestSessionExpires(t *testing.T) {
	s := newTestSession()
	payload := []byte("alice|" + strconv.FormatInt(time.Now().Add(-time.Second).Unix(), 10))
	expired := enc.EncodeToString(payload) + "." + s.mac(payload)

	if id, ok := readBack(s, expired); ok {
		t.Errorf("過期的 cookie 竟然通過了，拿到 id=%q", id)
	}
}

func TestCSRFTokenIsPerUser(t *testing.T) {
	s := newTestSession()
	if s.CSRFToken("alice") == s.CSRFToken("bob") {
		t.Error("不同使用者拿到同一個 CSRF token，等於沒有防護")
	}
	if s.CSRFToken("alice") != s.CSRFToken("alice") {
		t.Error("同一個使用者的 token 每次都不同，表單會一直失效")
	}
}
