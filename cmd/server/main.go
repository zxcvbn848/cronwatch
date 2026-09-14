// cronwatch：cron 該跑而沒跑的時候通知你。
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"cronwatch/internal/check"
	"cronwatch/internal/notify"
	"cronwatch/internal/user"
	"cronwatch/internal/web"
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

	checks := check.New(db)
	if err := checks.Migrate(ctx); err != nil {
		log.Fatalf("建表失敗: %v", err)
	}

	mailer := notify.FromEnv()
	if mailer == nil {
		log.Print("未設定 SMTP_HOST / NOTIFY_EMAIL，通知只會印在主控台")
	}

	go sweepLoop(ctx, checks, mailer)

	r := gin.Default()
	web.New(checks, user.New(db), mailer).Routes(r)

	log.Printf("監聽 :%s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatal(err)
	}
}

func sweepLoop(ctx context.Context, checks *check.Store, mailer *notify.Mailer) {
	t := time.NewTicker(sweepInterval)
	defer t.Stop()
	for range t.C {
		overdue, err := checks.Sweep(ctx)
		if err != nil {
			log.Printf("sweep 失敗: %v", err) // 下一輪自己會重試
			continue
		}
		for _, o := range overdue {
			log.Printf("逾期：%s (%s) 應在 %s 前回報", o.Name, o.ID, o.NextDueAt.Format(time.RFC3339))
			mailer.SendAsync("[cronwatch] 逾期："+o.Name,
				fmt.Sprintf("check %q (%s) 應在 %s 前回報心跳，但沒有收到。",
					o.Name, o.ID, o.NextDueAt.Format(time.RFC3339)))
		}
	}
}
