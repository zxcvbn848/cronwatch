# 部署到 VPS

以下以 Ubuntu、Docker Compose 與 Caddy 為例。cronwatch 目前設計為單一實例，
請勿同時啟動多個 app replica，否則可能重複寄送通知。

## 1. 準備設定

clone 專案後複製設定檔：

```bash
cp .env.example .env
```

在 `.env` 更換：

- `SESSION_SECRET`：使用 `openssl rand -hex 32` 產生。
- `POSTGRES_PASSWORD`：使用長且隨機、可安全放入 URL 的密碼。
- `SMTP_*`：填入正式 SMTP 服務；`SMTP_FROM` 必須是允許寄信的地址。
- `PORT`：建議設為 `127.0.0.1:8080`，只讓反向代理連線。

啟動：

```bash
docker compose up -d --build
```

## 2. 反向代理與 TLS

先讓網域的 DNS A/AAAA 紀錄指向 VPS，再安裝 Caddy，設定 `/etc/caddy/Caddyfile`：

```caddyfile
cron.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

Caddy 會自動取得並更新 TLS 憑證。套用設定：

```bash
sudo systemctl reload caddy
```

Caddy 會傳遞 `X-Forwarded-Proto: https`，cronwatch 因而產生 HTTPS Ping URL，
並替登入 Cookie 啟用 `Secure`。

## 3. 維護

升級：

```bash
git pull --ff-only && docker compose up -d --build
```

查看服務狀態：

```bash
docker compose ps
```

資料保存在 Docker volume `pgdata`。升級前應用 VPS 快照或 PostgreSQL 備份保護資料；
`docker compose down` 不會刪除 volume，但 `docker compose down -v` 會永久刪除資料。
