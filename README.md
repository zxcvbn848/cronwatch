# cronwatch

排程監控 — 你的 cron 該跑而沒跑的時候通知你。

> 開發中，尚未可用。規劃見 [`docs/PLAN.md`](docs/PLAN.md)。

## 為什麼

排程失敗是最沉默的故障類型。它不會噴錯、不會有 500、一般監控也看不到 ——
因為「什麼都沒發生」不是一個事件。等到有人發現報表沒出來，通常已經過了好幾天。

## 怎麼運作

Dead man's switch：

1. 建立一個 check，拿到專屬的 ping URL
2. 你的 cron 跑完後打那個 URL
3. 心跳在預期時間內沒來 → 發通知

## 狀態

| 里程碑 | 內容 | 狀態 |
|---|---|---|
| M1 | ping 端點 + 逾期偵測 | 完成 |
| M2 | Email 通知 | 完成 |
| M3 | 註冊登入與網頁介面 | 完成 |
| M4 | 開源發佈（Docker Compose 一鍵啟動） | 下一步 |
| M5 | 託管版 | — |

## 授權

[AGPL-3.0](LICENSE)

可以自由使用、修改、自架。但若把它（或修改版）提供成網路服務，
依 AGPL 第 13 條需一併提供你的原始碼。

這是刻意的選擇：自架版永遠免費且完整，而託管服務的收入用來支撐開發。

## 本機跑起來

```bash
docker compose up -d
DATABASE_URL=postgres://cronwatch:cronwatch@localhost:5432/cronwatch \
SMTP_HOST=localhost go run ./cmd/server
```

開 http://localhost:8080 註冊帳號，建一個 check，把詳情頁給的指令放進 crontab 最後一行：

```bash
0 3 * * * /path/to/job.sh && curl -fsS http://localhost:8080/ping/<id>
```

超過「週期 + 寬限」沒收到心跳就寄信給你，心跳回來再寄一次恢復通知。
只在狀態轉換時發，掛著的 cron 不會每分鐘寄一封。

## 設定

| 變數 | 預設 | 說明 |
|---|---|---|
| `DATABASE_URL` | — | 必填 |
| `PORT` | `8080` | |
| `SESSION_SECRET` | 隨機 | 沒設就每次啟動重新產生，也就是重啟會登出所有人 |
| `SMTP_HOST` | — | 沒設就不寄信，通知改印在主控台 |
| `SMTP_PORT` | `1025` | |
| `SMTP_USER` / `SMTP_PASS` | — | 留空則不做 SMTP 認證 |
| `SMTP_FROM` | `cronwatch@localhost` | |

通知寄給 check 的擁有者，不需要設收件人。

`docker compose up` 會一併起 [Mailpit](http://localhost:8025)，本機收信直接用它。

## 開發

```bash
TEST_DATABASE_URL=postgres://cronwatch:cronwatch@localhost:5432/cronwatch go test ./...
```

沒設 `TEST_DATABASE_URL` 的話需要 DB 的測試會 skip。邏輯大多在 SQL 裡，
mock 掉 DB 的測試等於什麼都沒測。

`docs/` 底下有可直接匯入 Postman 的 collection 與環境檔（只含 ping —— 那是唯一的對外 API）。

## 授權

[AGPL-3.0](LICENSE)

可以自由使用、修改、自架。但若把它（或修改版）提供成網路服務，
依 AGPL 第 13 條需一併提供你的原始碼。

這是刻意的選擇：自架版永遠免費且完整，而託管服務的收入用來支撐開發。

## 本機跑起來（M1）

```bash
docker compose up -d
DATABASE_URL=postgres://cronwatch:cronwatch@localhost:5432/cronwatch go run ./cmd/server
```

建一個 check，拿到 ping URL：

```bash
curl -X POST 'localhost:8080/checks?name=nightly&period=60&grace=10'
```

在你的 cron 最後打它：

```bash
curl -fsS localhost:8080/ping/<id>
```

超過 `period + grace` 沒收到心跳就發通知，心跳回來再發一次恢復通知。
只在狀態轉換時發，掛著的 cron 不會每分鐘寄一封。

## 通知設定

沒設定 SMTP 的話通知只印在主控台，本機開發不用被迫架信箱。

| 變數 | 預設 | 說明 |
|---|---|---|
| `SMTP_HOST` | — | 沒設就不寄信 |
| `SMTP_PORT` | `1025` | |
| `SMTP_USER` / `SMTP_PASS` | — | 留空則不做 SMTP 認證 |
| `SMTP_FROM` | `cronwatch@localhost` | |
| `NOTIFY_EMAIL` | — | 收件人。沒設就不寄信 |

`docker compose up` 會一併起 [Mailpit](http://localhost:8025)，本機收信直接用它：

```bash
SMTP_HOST=localhost NOTIFY_EMAIL=you@example.com \
DATABASE_URL=postgres://cronwatch:cronwatch@localhost:5432/cronwatch go run ./cmd/server
```

收件人目前是單一全域位址 —— M3 有了帳號之後改成 check 擁有者的 email。

跑測試（需要 Postgres）：

```bash
TEST_DATABASE_URL=postgres://cronwatch:cronwatch@localhost:5432/cronwatch go test ./...
```
