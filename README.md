# cronwatch

你的 cron 該跑而沒跑時通知你。

cronwatch 是一個可自架的 dead man's switch：建立 check、取得專屬 Ping URL，
再讓 cron 成功執行後呼叫它。超過「週期 + 寬限」沒收到心跳時會寄信，恢復時再通知一次。

## 五分鐘啟動

需要 Docker 與 Docker Compose。

```bash
cp .env.example .env
```

先編輯 `.env`，至少更換 `SESSION_SECRET` 與 `POSTGRES_PASSWORD`，再啟動：

```bash
docker compose up -d --build
```

開啟 http://localhost:8080 註冊帳號。開發用信件會出現在
[Mailpit](http://localhost:8025)。

建立 check 後，把詳情頁提供的指令接在 cron 工作後面：

```bash
0 3 * * * /path/to/job.sh && curl -fsS http://localhost:8080/ping/<id>
```

升級時拉取新版並重新建置：

```bash
git pull --ff-only && docker compose up -d --build
```

## 設定

| 變數 | 預設 | 說明 |
|---|---|---|
| `DATABASE_URL` | Compose 自動設定 | PostgreSQL 連線字串；直接執行 binary 時必填 |
| `PORT` | `8080` | 對外 HTTP port |
| `DB_PORT` | `5432` | PostgreSQL 綁定的本機 port |
| `MAILPIT_SMTP_PORT` / `MAILPIT_UI_PORT` | `1025` / `8025` | Mailpit 綁定的本機 port |
| `SESSION_SECRET` | — | 正式環境必填，建議用 `openssl rand -hex 32` 產生 |
| `SMTP_HOST` | `mail` | SMTP 主機；空白時只將通知印到 log |
| `SMTP_PORT` | `1025` | SMTP port |
| `SMTP_USER` / `SMTP_PASS` | — | SMTP 認證；留空則不認證 |
| `SMTP_FROM` | `cronwatch@localhost` | 寄件者 |

正式環境的反向代理、TLS 與 SMTP 設定見 [部署文件](docs/DEPLOY.md)。

## 開發

只啟動 PostgreSQL 與 Mailpit：

```bash
docker compose up -d db mail
```

PostgreSQL 只綁定本機的 `127.0.0.1:5432`。執行 Go 程式：

```bash
DATABASE_URL=postgres://cronwatch:change-me@localhost:5432/cronwatch SMTP_HOST=localhost go run ./cmd/server
```

測試：

```bash
TEST_DATABASE_URL=postgres://cronwatch:change-me@localhost:5432/cronwatch go test ./...
```

## 狀態

- M1：Ping 端點與逾期偵測（完成）
- M2：Email 通知（完成）
- M3：註冊登入與網頁介面（完成）
- M4：Docker Compose 自架發佈（進行中）
- M5：託管版（尚未開始）

## 授權

[AGPL-3.0](LICENSE)。可以自由使用、修改與自架；若將本程式或修改版提供成網路服務，
依 AGPL 第 13 條需一併提供原始碼。
