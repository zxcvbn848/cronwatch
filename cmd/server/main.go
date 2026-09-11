// cronwatch M1：ping 端點 + 逾期偵測迴圈。沒有認證、沒有 UI、沒有通知。
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"cronwatch/internal/check"
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

	go sweepLoop(ctx, store)

	r := gin.Default()

	// PLAN.md 決定 #2：最便宜的端點。一句 UPDATE，回 200 空 body。
	r.Any("/ping/:id", func(c *gin.Context) {
		found, err := store.Ping(c.Request.Context(), c.Param("id"))
		switch {
		case err != nil:
			log.Printf("ping %s 失敗: %v", c.Param("id"), err)
			c.Status(http.StatusInternalServerError)
		case !found:
			c.Status(http.StatusNotFound)
		default:
			c.Status(http.StatusOK)
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
		id, err := store.Create(c.Request.Context(), name, period, grace)
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

func sweepLoop(ctx context.Context, store *check.Store) {
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
		}
	}
}
