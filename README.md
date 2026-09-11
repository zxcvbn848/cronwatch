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
| M1 | ping 端點 + 逾期偵測 | 進行中 |
| M2 | Email 通知 | — |
| M3 | 註冊登入與網頁介面 | — |
| M4 | 開源發佈（Docker Compose 一鍵啟動） | — |
| M5 | 託管版 | — |

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

超過 `period + grace` 沒收到心跳，主控台會印出逾期訊息。M1 還不會寄信。

跑測試（需要 Postgres）：

```bash
TEST_DATABASE_URL=postgres://cronwatch:cronwatch@localhost:5432/cronwatch go test ./...
```
