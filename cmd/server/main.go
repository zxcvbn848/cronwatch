// cronwatch M1：ping 端點 + 逾期偵測迴圈。沒有認證、沒有 UI、沒有通知。
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"cronwatch/internal/check"
	"cronwatch/internal/notify"
)

const sweepInterval = 10 * time.Second

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("需要 DATABASE_URL")
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	ctx := context.Background()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("連線失敗: %v", err)
	}
	defer db.Close()

	store := check.New(db)
	if err := store.Migrate(ctx); err != nil {
		log.Fatalf("建表失敗: %v", err)
	}

	mailer := notify.FromEnv()
	if mailer == nil {
		log.Print("未設定 SMTP_HOST / NOTIFY_EMAIL，通知只會印在主控台")
	}

	go sweepLoop(ctx, store, mailer)

	r := gin.Default()

	// PLAN.md 決定 #2：最便宜的端點。一句 UPDATE，回 200 空 body。
	r.Any("/ping/:id", func(c *gin.Context) {
		id := c.Param("id")
		found, recovered, name, err := store.Ping(c.Request.Context(), id,
			c.ClientIP(), c.Request.UserAgent())
		switch {
		case err != nil:
			log.Printf("ping %s 失敗: %v", id, err)
			c.Status(http.StatusInternalServerError)
			return
		case !found:
			c.Status(http.StatusNotFound)
			return
		}
		c.Status(http.StatusOK)
		if recovered {
			// 非同步，寄信不能拖慢 ping —— 這是流量最高的端點
			go send(mailer, "[cronwatch] 恢復："+name,
				fmt.Sprintf("check %q (%s) 在 %s 重新回報心跳。",
					name, id, time.Now().Format(time.RFC3339)))
		}
	})

	// ponytail: 無認證的臨時端點，只為了讓 M1 能 demo。
	// M3 接上 session 後改成帶 user_id 的表單。
	r.POST("/checks", func(c *gin.Context) {
		name := c.Query("name")
		if name == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "需要 name"})
			return
		}
		period, err := strconv.Atoi(c.DefaultQuery("period", "0"))
		if err != nil || period <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "period 需要是正整數秒數"})
			return
		}
		grace, err := strconv.Atoi(c.DefaultQuery("grace", "60"))
		if err != nil || grace < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "grace 需要是非負整數秒數"})
			return
		}
		// userID 空字串 = 不屬於任何人。步驟 2 接上認證後這個端點會被移除。
		id, err := store.Create(c.Request.Context(), "", name, period, grace)
		if err != nil {
			log.Printf("建立 check 失敗: %v", err)
			c.Status(http.StatusInternalServerError)
			return
		}
		c.JSON(http.StatusCreated, gin.H{"id": id, "ping_url": "/ping/" + id})
	})

	log.Printf("監聽 :%s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatal(err)
	}
}

// send 包一層只為了統一 log 寄信失敗。失敗就算了，沒有重試佇列。
func send(m *notify.Mailer, subject, body string) {
	if err := m.Send(subject, body); err != nil {
		log.Print(err)
	}
}

func sweepLoop(ctx context.Context, store *check.Store, mailer *notify.Mailer) {
	t := time.NewTicker(sweepInterval)
	defer t.Stop()
	for range t.C {
		overdue, err := store.Sweep(ctx)
		if err != nil {
			log.Printf("sweep 失敗: %v", err) // 下一輪自己會重試
			continue
		}
		for _, o := range overdue {
			log.Printf("逾期：%s (%s) 應在 %s 前回報", o.Name, o.ID, o.NextDueAt.Format(time.RFC3339))
			// ponytail: 每封信一個 goroutine。SMTP 沒有 dial timeout，
			// 同步寄會讓卡住的信件凍結整個偵測迴圈。量大要改成有界的 worker。
			go send(mailer, "[cronwatch] 逾期："+o.Name,
				fmt.Sprintf("check %q (%s) 應在 %s 前回報心跳，但沒有收到。",
					o.Name, o.ID, o.NextDueAt.Format(time.RFC3339)))
		}
	}
}
