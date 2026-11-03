// Package web 是 HTTP 層：路由、模板、session。
//
// 沒有 service / repository 分層 —— handler 直接呼叫 store。
// 這個規模不需要中間人，等 handler 真的開始重複邏輯時再說。
package web

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"cronwatch/internal/check"
	"cronwatch/internal/notify"
	"cronwatch/internal/user"
)

//go:embed templates/*.html
var templates embed.FS

type Handler struct {
	checks *check.Store
	users  *user.Store
	sess   *Session
	mailer *notify.Mailer
}

func New(checks *check.Store, users *user.Store, mailer *notify.Mailer) *Handler {
	return &Handler{checks: checks, users: users, sess: NewSession(), mailer: mailer}
}

// Routes 掛上所有路由。
func (h *Handler) Routes(r *gin.Engine) {
	r.SetHTMLTemplate(template.Must(template.ParseFS(templates, "templates/*.html")))

	// ping 不需要認證：uuid 本身就是憑證
	r.Any("/ping/:id", h.ping)

	r.GET("/register", h.registerForm)
	r.POST("/register", h.register)
	r.GET("/login", h.loginForm)
	r.POST("/login", h.login)

	in := r.Group("/", h.sess.RequireAuth())
	in.POST("/logout", h.logout)
	in.GET("/", h.list)
	in.GET("/checks/new", h.newForm)
	in.GET("/checks/rows", h.rows)
	in.POST("/checks", h.create)
	in.GET("/checks/:id", h.detail)
	in.POST("/checks/:id", h.update)
	in.POST("/checks/:id/pause", h.togglePause)
	in.POST("/checks/:id/delete", h.destroy)
}

// page 組出每個模板都需要的共用欄位。
func (h *Handler) page(c *gin.Context, extra gin.H) gin.H {
	data := gin.H{}
	for k, v := range extra {
		data[k] = v
	}
	if id, ok := h.sess.userID(c); ok {
		email, err := h.users.Email(c.Request.Context(), id)
		if err != nil {
			log.Printf("取 email 失敗: %v", err)
		}
		data["Email"] = email
		data["CSRF"] = h.sess.CSRFToken(id)
	}
	return data
}

func (h *Handler) registerForm(c *gin.Context) {
	c.HTML(http.StatusOK, "register.html", h.page(c, nil))
}

func (h *Handler) register(c *gin.Context) {
	email, password := c.PostForm("email"), c.PostForm("password")
	id, err := h.users.Create(c.Request.Context(), email, password)
	switch {
	case errors.Is(err, user.ErrDuplicate),
		errors.Is(err, user.ErrInvalidEmail),
		errors.Is(err, user.ErrWeakPassword):
		c.HTML(http.StatusBadRequest, "register.html",
			h.page(c, gin.H{"Err": err.Error(), "Email0": email}))
		return
	case err != nil:
		log.Printf("註冊失敗: %v", err)
		c.HTML(http.StatusInternalServerError, "register.html",
			h.page(c, gin.H{"Err": "系統錯誤，請稍後再試", "Email0": email}))
		return
	}
	h.sess.Issue(c, id) // 註冊完直接登入，不用再打一次密碼
	c.Redirect(http.StatusSeeOther, "/")
}

func (h *Handler) loginForm(c *gin.Context) {
	c.HTML(http.StatusOK, "login.html", h.page(c, nil))
}

func (h *Handler) login(c *gin.Context) {
	email, password := c.PostForm("email"), c.PostForm("password")
	id, ok, err := h.users.Authenticate(c.Request.Context(), email, password)
	if err != nil {
		log.Printf("登入失敗: %v", err)
		c.HTML(http.StatusInternalServerError, "login.html",
			h.page(c, gin.H{"Err": "系統錯誤，請稍後再試", "Email0": email}))
		return
	}
	if !ok {
		// 不區分「帳號不存在」與「密碼錯誤」
		c.HTML(http.StatusUnauthorized, "login.html",
			h.page(c, gin.H{"Err": "email 或密碼不正確", "Email0": email}))
		return
	}
	h.sess.Issue(c, id)
	c.Redirect(http.StatusSeeOther, "/")
}

func (h *Handler) logout(c *gin.Context) {
	h.sess.Clear(c)
	c.Redirect(http.StatusSeeOther, "/login")
}

// ping 是流量最高的端點：一次 DB 來回，回 200 空 body。
func (h *Handler) ping(c *gin.Context) {
	id := c.Param("id")
	r, err := h.checks.Ping(c.Request.Context(), id, c.ClientIP(), c.Request.UserAgent())
	switch {
	case err != nil:
		log.Printf("ping %s 失敗: %v", id, err)
		c.Status(http.StatusInternalServerError)
		return
	case !r.Found:
		c.Status(http.StatusNotFound)
		return
	}
	c.Status(http.StatusOK)
	if r.Recovered {
		h.mailer.SendAsync(r.Email, "[cronwatch] 恢復："+r.Name,
			fmt.Sprintf("check %q (%s) 在 %s 重新回報心跳。",
				r.Name, id, time.Now().Format(time.RFC3339)))
	}
}
