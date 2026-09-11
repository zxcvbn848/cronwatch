# cronwatch — 規劃

排程監控服務。工作名稱，要改再改。

## 一句話

**你的 cron 該跑而沒跑的時候通知你。**

排程失敗是最沉默的故障類型 —— 它不會噴錯、不會有 500、監控也看不到，
因為「什麼都沒發生」不是一個事件。等到有人發現報表沒出來，通常已經過了好幾天。

## 核心機制：dead man's switch

```
1. 使用者建立一個 check          → 拿到一個專屬的 ping URL
2. 他的 cron 跑完後打那個 URL     → 我們記下「最後一次心跳」
3. 心跳在預期時間內沒來           → 發通知
```

整個產品就這三件事。**判斷依據是「沒有東西發生」，不是「有錯誤發生」** ——
這是它和一般監控的根本差別，也是它存在的理由。

## 為什麼選這個

- **範圍小到能被一個人做完。** 多數側專案死在範圍，不是技術
- **買家是所有有 cron 的公司**，而那是全部的公司
- **Go 的優勢是真的**：常駐偵測、時間精度、單機撐大量檢查、單一執行檔讓自架無痛
- **有已驗證的商業模式可以照抄**：`Healthchecks.io` 是一個人做的，開源 + 付費託管，長期獲利

## MVP 範圍

能收錢的最小集合：

- [ ] 註冊 / 登入（email + 密碼）
- [ ] 建立 check：名稱、預期週期（每 N 分鐘）、寬限期
- [ ] Ping 端點：`POST|GET /ping/{uuid}`，無需認證
- [ ] 偵測逾期並轉為 down
- [ ] Email 通知（狀態轉換時各發一次：down、恢復）
- [ ] 列表頁：狀態、最後心跳時間、逾期倒數
- [ ] 單一 check 的心跳歷史（最近 N 筆）

## 明確不做（MVP 階段）

刻意延後，每一項都附觸發條件：

| 不做 | 為什麼 | 什麼時候做 |
|---|---|---|
| Cron 運算式 | 「每 N 分鐘」已涵蓋多數情境（每日 3 點 = 週期 24h + 寬限 1h） | 有人要求「只有週一到五」或「每月 1 號」 |
| 多通知管道 | Email 先做完再說。Telegram / Slack / Webhook 是後續 | 有付費使用者開口 |
| 執行時長追蹤（`/start` 訊號） | 要多一張表與更多狀態 | 核心穩定之後 |
| 團隊 / 組織 | 單人帳號先跑通 | 有公司客戶 |
| 計費 | 沒有使用者之前寫計費是純浪費 | 有人問「怎麼付錢」 |
| 水平擴充 | 見下方「已知上限」 | 單機撐不住 |

## 資料模型（最小）

```
users
  id, email, password_hash, created_at

checks
  id            uuid        ← 同時是 ping URL 的路徑
  user_id
  name
  period_secs   int         ← 預期多久收到一次心跳
  grace_secs    int         ← 寬限，避免抖動誤報
  status        enum        ← new | up | down | paused
  last_ping_at  timestamp
  next_due_at   timestamp   ← 關鍵欄位，見下方
  created_at, updated_at

pings
  id, check_id, received_at, remote_addr, user_agent
  ← 只留最近 N 筆，超過就裁掉
```

## 三個關鍵技術決定

### 1. 偵測用 `next_due_at` 索引，不是掃全表

天真的做法是每分鐘掃所有 check、逐個算「該不該逾期」。check 數量一多就爆。

做法是把「下次該收到心跳的時間」算出來存成欄位：

```
收到 ping → last_ping_at = now
            next_due_at  = now + period_secs + grace_secs
            status       = up
```

偵測迴圈只需要一個走索引的查詢：

```sql
SELECT * FROM checks
 WHERE status = 'up' AND next_due_at <= now()
```

在 `(status, next_due_at)` 上建索引，**成本與逾期的數量成正比，而不是與總 check 數成正比**。
正常狀況下這個查詢回 0 列。

### 2. Ping 端點必須極便宜

這是流量最高的端點 —— 每個使用者的每個 cron 每次執行都會打一次，而且不需要登入。

- **一次 UPDATE 解決**，不查詢、不 join、不寫 log 表（歷史用非同步寫入或抽樣）
- **uuid 直接當主鍵查詢**，不經過 user 驗證
- 回 200 就好，不回 body

如果 ping 端點慢，整個服務的成本結構就毀了。

### 3. 通知只在狀態轉換時發

不做這件事的話，一個掛掉的 cron 會每分鐘寄一封信。

```
up   → down   發「掛了」
down → up     發「恢復了」
其他轉換       不發
```

狀態存在 `checks.status`，轉換由偵測迴圈與 ping 處理器各自負責一半。
這是整個系統唯一需要小心競態的地方。

## 技術選型

| 層 | 選擇 | 理由 |
|---|---|---|
| 語言 | Go | 常駐偵測、單一執行檔、低記憶體 |
| HTTP | Gin | 已熟悉，不在這裡冒險 |
| DB | **PostgreSQL** | partial index（`WHERE status='up'`）比 MySQL 乾淨，且雲端便宜選項多（Neon / Supabase） |
| 前端 | **Go template + htmx** | 沒有 build step、沒有 SPA 狀態管理。這個產品的介面就是列表 + 表單，SPA 是純負擔 |
| 排程 | 同一個 binary 內的 goroutine | 單一執行檔是自架能成立的前提 |
| 部署 | Docker，單一 binary + Postgres | Fly.io / Railway / 便宜 VPS 都能跑 |

**前端刻意不用 Vue。** 你會寫，但 SPA 會帶來 build、路由、狀態管理、API 契約四份額外工作，
而這個產品的畫面複雜度撐不起那些成本。

## 已知上限（ponytail）

寫在這裡，以後撞到才不會以為是 bug：

- **單一實例。** 偵測迴圈沒有分散式鎖，跑兩份會發兩次通知。要水平擴充得用
  `SELECT ... FOR UPDATE SKIP LOCKED` 或外部鎖
- **`pings` 表會無限成長。** MVP 靠定期裁切，量大要改分區或時序資料庫
- **通知只有 email。** 寄信失敗目前沒有重試佇列
- **週期只支援固定間隔。** 不規則排程（工作日、月初）要等 cron 運算式

## 里程碑

每一階段都要能跑、能 demo，不做「先蓋架構後面再接起來」。

**M1 — 骨幹能通（最重要）**
`ping` 端點 + `checks` 表 + 偵測迴圈 + 主控台印出「某個 check 逾期了」。
沒有註冊、沒有 UI、沒有通知。目的是證明核心機制成立。

**M2 — 會發信**
Email 通知 + 狀態轉換邏輯 + 恢復通知。

**M3 — 有人能用**
註冊登入、列表頁、建立/編輯/暫停 check、心跳歷史。

**M4 — 開源發佈**
README、Docker Compose 一鍵啟動、部署文件。
**自架版本要先能讓陌生人裝起來**，這個模式的分發優勢才成立。

**M5 — 託管版**
自己的實例上線、加計費。只有在 M4 有人真的裝了之後才做。

## 商業模式

**開源核心 + 付費託管。**

- 自架版：完整功能，免費。這是行銷管道，不是慈善
- 託管版：按 check 數量分級距。免費方案給少量 check 讓人試
- 企業：自架 + 支援合約（這通常才是真正的收入）

工程師不付錢，公司付錢 —— 所以定價與功能都往「公司要什麼」設計（多使用者、
稽核紀錄、SSO 是後期的付費觸發點）。

## 第一步

M1，而且只做 M1。三個檔案就能跑起來：

```
cmd/server/main.go     HTTP + 偵測 goroutine
internal/check/        資料模型與 ping / 逾期判斷
migrations/            建表
```

不要在 M1 就分 controller / service / repository 四層 —— 那是等程式碼長到需要分層時
再分。現在分只是把三個檔案的東西攤成十二個。
