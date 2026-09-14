package web

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"cronwatch/internal/check"
)

const historyLimit = 50

// row 是畫面用的 check。所有格式化都在 Go 這邊做完，模板保持笨。
type row struct {
	check.Check
	StatusText string
	LastPing   string
	Due        string
	PingURL    string
}

// heartbeat 是畫面用的心跳紀錄。
type heartbeat struct {
	check.Heartbeat
	When string
}

var statusText = map[string]string{
	"new": "等待第一次心跳", "up": "正常", "down": "掛了", "paused": "已暫停",
}

// human 把時間長度講成人話。只要一個量級就夠看了。
func human(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d 秒", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d 分鐘", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小時", int(d.Hours()))
	default:
		return fmt.Sprintf("%d 天", int(d.Hours()/24))
	}
}

func (h *Handler) toRow(c *gin.Context, ch check.Check) row {
	r := row{Check: ch, StatusText: statusText[ch.Status], LastPing: "—", Due: "—"}

	scheme := "http"
	if c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	r.PingURL = scheme + "://" + c.Request.Host + "/ping/" + ch.ID

	if ch.LastPingAt != nil {
		r.LastPing = human(time.Since(*ch.LastPingAt)) + "前"
	}
	switch {
	case ch.Status == "paused":
		r.Due = "已暫停"
	case ch.NextDueAt == nil:
	case time.Now().Before(*ch.NextDueAt):
		r.Due = "還有 " + human(time.Until(*ch.NextDueAt))
	default:
		r.Due = "逾期 " + human(time.Since(*ch.NextDueAt))
	}
	return r
}

func (h *Handler) rowsFor(c *gin.Context, userID string) ([]row, error) {
	list, err := h.checks.ListByUser(c.Request.Context(), userID)
	if err != nil {
		return nil, err
	}
	rows := make([]row, 0, len(list))
	for _, ch := range list {
		rows = append(rows, h.toRow(c, ch))
	}
	return rows, nil
}

func (h *Handler) list(c *gin.Context) {
	rows, err := h.rowsFor(c, currentUser(c))
	if err != nil {
		log.Printf("列表失敗: %v", err)
		c.String(http.StatusInternalServerError, "系統錯誤")
		return
	}
	c.HTML(http.StatusOK, "list.html", h.page(c, gin.H{"Rows": rows}))
}

// rows 只回表格列，給 htmx 每 15 秒換掉 tbody 用。
func (h *Handler) rows(c *gin.Context) {
	rows, err := h.rowsFor(c, currentUser(c))
	if err != nil {
		log.Printf("列表失敗: %v", err)
		c.Status(http.StatusInternalServerError)
		return
	}
	c.HTML(http.StatusOK, "rows.html", gin.H{"Rows": rows})
}

func (h *Handler) newForm(c *gin.Context) {
	c.HTML(http.StatusOK, "form.html", h.page(c, gin.H{"Period0": 1440, "Grace0": 30}))
}

// form 讀出並驗證建立/編輯共用的三個欄位。週期以分鐘輸入，存進 DB 是秒。
func form(c *gin.Context) (name string, period, grace int, err string) {
	name = c.PostForm("name")
	if name == "" {
		return "", 0, 0, "名稱不能空白"
	}
	p, e1 := strconv.Atoi(c.PostForm("period"))
	g, e2 := strconv.Atoi(c.PostForm("grace"))
	if e1 != nil || p < 1 {
		return name, 0, 0, "週期需要是 1 以上的分鐘數"
	}
	if e2 != nil || g < 0 {
		return name, 0, 0, "寬限需要是 0 以上的分鐘數"
	}
	return name, p * 60, g * 60, ""
}

func (h *Handler) create(c *gin.Context) {
	name, period, grace, bad := form(c)
	if bad != "" {
		c.HTML(http.StatusBadRequest, "form.html", h.page(c, gin.H{
			"Err": bad, "Name0": name,
			"Period0": c.PostForm("period"), "Grace0": c.PostForm("grace"),
		}))
		return
	}
	id, err := h.checks.Create(c.Request.Context(), currentUser(c), name, period, grace)
	if err != nil {
		log.Printf("建立 check 失敗: %v", err)
		c.String(http.StatusInternalServerError, "系統錯誤")
		return
	}
	c.Redirect(http.StatusSeeOther, "/checks/"+id) // 導到詳情頁，使用者要的是 ping URL
}

func (h *Handler) detail(c *gin.Context) {
	h.renderDetail(c, http.StatusOK, "")
}

func (h *Handler) renderDetail(c *gin.Context, code int, errMsg string) {
	user := currentUser(c)
	ch, ok, err := h.checks.GetForUser(c.Request.Context(), c.Param("id"), user)
	if err != nil {
		log.Printf("取 check 失敗: %v", err)
		c.String(http.StatusInternalServerError, "系統錯誤")
		return
	}
	if !ok {
		c.String(http.StatusNotFound, "找不到這個 check")
		return
	}
	pings, err := h.checks.RecentPings(c.Request.Context(), ch.ID, user, historyLimit)
	if err != nil {
		log.Printf("取心跳歷史失敗: %v", err)
		c.String(http.StatusInternalServerError, "系統錯誤")
		return
	}
	hb := make([]heartbeat, 0, len(pings))
	for _, p := range pings {
		hb = append(hb, heartbeat{Heartbeat: p, When: p.ReceivedAt.Local().Format("01/02 15:04:05")})
	}
	c.HTML(code, "detail.html", h.page(c, gin.H{
		"Row":       h.toRow(c, ch),
		"PeriodMin": ch.PeriodSecs / 60,
		"GraceMin":  ch.GraceSecs / 60,
		"Pings":     hb,
		"Err":       errMsg,
	}))
}

func (h *Handler) update(c *gin.Context) {
	name, period, grace, bad := form(c)
	if bad != "" {
		h.renderDetail(c, http.StatusBadRequest, bad)
		return
	}
	ok, err := h.checks.UpdateForUser(c.Request.Context(), c.Param("id"), currentUser(c), name, period, grace)
	h.afterWrite(c, ok, err, "/checks/"+c.Param("id"))
}

func (h *Handler) togglePause(c *gin.Context) {
	ok, err := h.checks.TogglePauseForUser(c.Request.Context(), c.Param("id"), currentUser(c))
	h.afterWrite(c, ok, err, "/checks/"+c.Param("id"))
}

func (h *Handler) destroy(c *gin.Context) {
	ok, err := h.checks.DeleteForUser(c.Request.Context(), c.Param("id"), currentUser(c))
	h.afterWrite(c, ok, err, "/")
}

// afterWrite：寫入失敗是 500，動不到（不存在或不是你的）是 404，成功就轉頁。
func (h *Handler) afterWrite(c *gin.Context, ok bool, err error, to string) {
	switch {
	case err != nil:
		log.Printf("寫入 check 失敗: %v", err)
		c.String(http.StatusInternalServerError, "系統錯誤")
	case !ok:
		c.String(http.StatusNotFound, "找不到這個 check")
	default:
		c.Redirect(http.StatusSeeOther, to)
	}
}
