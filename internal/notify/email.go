// Package notify 發通知。M2 只有 email。
package notify

import (
	"fmt"
	"log"
	"mime"
	"net/smtp"
	"os"
	"strings"
	"time"
)

// Mailer 是設定好的 SMTP 寄件者。零設定時 FromEnv 回 nil，
// 此時 Send 只印 log 不寄信 —— 本機開發不用被迫架 SMTP。
type Mailer struct {
	addr, from, to string
	auth           smtp.Auth
}

// FromEnv 從環境變數建 Mailer。缺 SMTP_HOST 或 NOTIFY_EMAIL 就回 nil。
//
// ponytail: 收件人是單一全域位址。M3 有了 users 表之後改成 check 擁有者的 email。
func FromEnv() *Mailer {
	host, to := os.Getenv("SMTP_HOST"), os.Getenv("NOTIFY_EMAIL")
	if host == "" || to == "" {
		return nil
	}
	port := os.Getenv("SMTP_PORT")
	if port == "" {
		port = "1025"
	}
	from := os.Getenv("SMTP_FROM")
	if from == "" {
		from = "cronwatch@localhost"
	}
	m := &Mailer{addr: host + ":" + port, from: from, to: to}
	if user := os.Getenv("SMTP_USER"); user != "" {
		m.auth = smtp.PlainAuth("", user, os.Getenv("SMTP_PASS"), host)
	}
	return m
}

// SendAsync 非同步寄信，失敗自己 log。
//
// ponytail: 每封信一個 goroutine。net/smtp 沒有 dial timeout，同步寄的話
// 一封卡住的信會凍結偵測迴圈（= 漏掉後續所有逾期），在 ping 路徑上則會
// 拖慢最熱的端點。量大要改成有界的 worker。
func (m *Mailer) SendAsync(subject, body string) {
	go func() {
		if err := m.Send(subject, body); err != nil {
			log.Print(err)
		}
	}()
}

// Send 寄一封純文字信。nil receiver 是合法的：印 log 就好。
//
// ponytail: 寄失敗只回 error 由呼叫端 log，沒有重試佇列。
// 要重試的話得先有一張 outbox 表。
func (m *Mailer) Send(subject, body string) error {
	if m == nil {
		log.Printf("[notify 未設定] %s\n%s", subject, body)
		return nil
	}
	msg := strings.Join([]string{
		"From: " + m.from,
		"To: " + m.to,
		// 主旨有中文，不編碼會變亂碼
		"Subject: " + mime.QEncoding.Encode("UTF-8", subject),
		"Date: " + time.Now().Format(time.RFC1123Z),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=\"UTF-8\"",
		"",
		body,
	}, "\r\n")
	if err := smtp.SendMail(m.addr, m.auth, m.from, []string{m.to}, []byte(msg)); err != nil {
		return fmt.Errorf("寄信到 %s 失敗: %w", m.to, err)
	}
	return nil
}
