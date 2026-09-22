# cronwatch — 開發交接

> 給接手的人或 agent。產品規劃看 [`docs/PLAN.md`](docs/PLAN.md)，
> 使用方式看 [`README.md`](README.md)。這份講的是「現在長什麼樣、為什麼這樣、接下來做什麼」。

## 一句話

Dead man's switch：cron 跑完打一個 URL，心跳在預期時間內沒來就寄信。
判斷依據是「**沒有東西發生**」，不是「有錯誤發生」—— 這是它和一般監控的根本差別。

## 技術棧

| 層 | 選擇 | 備註 |
|---|---|---|
| 語言 | Go 1.25 | |
| HTTP | Gin v1.12 | |
| DB | PostgreSQL 17 | partial index 是核心機制的前提 |
| DB driver | pgx/v5（pgxpool） | 不用 ORM，SQL 直接寫 |
| 密碼 | `x/crypto/bcrypt` | gin 的間接依賴，不是新模組 |
| 前端 | Go html/template + htmx | 沒有 build step，沒有 npm |
| 排程 | 同一個 binary 內的 goroutine | 單一執行檔是自架能成立的前提 |

**直接依賴只有兩個**：`gin-gonic/gin`、`jackc/pgx/v5`。加第三個之前先想清楚。

## 目錄

```
cmd/server/main.go          98 行，純 wiring + sweep/trim 兩個迴圈
internal/check/
  check.go                 288 行，所有 SQL 都在這
  schema.sql               //go:embed，啟動時無條件跑
internal/user/user.go       85 行，bcrypt 註冊與驗證
internal/web/
  web.go                   149 行，路由 + 認證頁
  checks.go                212 行，check 的 CRUD handler
  session.go               125 行，HMAC cookie + CSRF + RequireAuth
  templates/*.html         7 個，//go:embed
docs/                      PLAN.md、Postman collection 與環境檔
```

**沒有 service / repository 分層。** handler 直接呼叫 store。這個規模不需要中間人，
等 handler 真的開始重複邏輯時再分。1400 行的專案攤成四層只是把東西藏起來。

## 四個必須知道的設計

### 1. 授權邊界在 `internal/check`

除了 `Ping`，每個方法都帶 `userID`，而且**刻意不提供「只靠 id 取 check」的方法**
—— 有那個方法，總有一天某個 handler 會用它。

```
ListByUser / GetForUser / RecentPings
UpdateForUser / TogglePauseForUser / DeleteForUser
Ping ← 唯一例外，uuid 本身就是憑證
```

查不到與不是你的都回 `found=false`，不區分 —— 區分了就洩漏他人 id 的存在。
**新增任何 check 相關的查詢時，照這個模式帶 userID。**
`TestUserScoping` 會抓到破口。

### 2. 偵測靠 partial index，不掃全表

```sql
CREATE INDEX checks_due ON checks (next_due_at) WHERE status = 'up';
```

`Sweep` 是一句 `UPDATE ... RETURNING`，同時完成「找出逾期」與「標記 down」。
**成本與逾期的數量成正比，不是與總 check 數成正比**，正常狀況回 0 列。

這句 SQL 天生冪等：已經是 down 的不會再被回傳。**「通知只在狀態轉換時發」
完全靠這個性質**，沒有額外狀態。改它之前先想清楚會不會變成每分鐘寄一封信。

### 3. Ping 是全站最熱的端點

每個使用者的每個 cron 每次執行都會打一次，而且不需要認證。
目前是**一次 DB 來回**，用 CTE 同時做三件事：更新 checks、取得更新前的 status
（自我 join 一份 `old`，因為 `RETURNING` 只給得到新值）、寫入 pings。

Postgres 保證 `WITH` 裡的寫入語句一定執行，即使主查詢沒有引用它。

**不要在這條路徑上加查詢、加 join、加同步寄信。** 寄信一律 `SendAsync`。

### 4. 狀態機

```
new ──ping──> up ──逾期──> down ──ping──> up
                 ↕ 暫停
               paused
```

`paused` 不在 `Sweep` 的 `WHERE status='up'` 裡，所以立刻安靜。
**ping 會把 paused 叫醒**（跟 Healthchecks.io 一致）：暫停的語意是「我知道它關著，
別吵我」，心跳回來就表示它又開著了。

## 路由

```
ANY  /ping/:id              不需認證
GET/POST /register /login
POST /logout
GET  /                      列表
GET  /checks/new            建立表單
GET  /checks/rows           htmx 局部刷新（每 15 秒）
POST /checks                建立
GET  /checks/:id            詳情 + 心跳歷史
POST /checks/:id            更新
POST /checks/:id/pause      切換暫停
POST /checks/:id/delete
```

`RequireAuth` 同時做認證與 CSRF 比對。**前提是所有改變狀態的操作都是 POST，
沒有任何 GET 會改資料** —— 加新路由時守住這條。

## 跑起來

```bash
docker compose up -d      # postgres + mailpit
DATABASE_URL=postgres://cronwatch:cronwatch@localhost:5432/cronwatch \
SMTP_HOST=localhost go run ./cmd/server
```

Mailpit 的信箱 UI 在 http://localhost:8025，通知都會進那裡。

設定全在環境變數，看 README 的表。`SESSION_SECRET` 沒設就隨機產生（重啟會登出所有人）。

## 測試

```bash
TEST_DATABASE_URL=postgres://cronwatch:cronwatch@localhost:5432/cronwatch go test ./...
```

沒設 `TEST_DATABASE_URL` 的話需要 DB 的測試會 skip。
**不 mock DB** —— 邏輯大多在 SQL 裡，mock 掉等於什麼都沒測。

九個測試，只覆蓋真的會出事的路徑：

| 測試 | 守什麼 |
|---|---|
| `TestUserScoping` | 授權邊界。壞了就是資料外洩 |
| `TestSessionRejectsTampering` | 五種偽造 cookie 的方式。壞了等於任何人都能登入成任何人 |
| `TestSessionExpires` / `TestCSRFTokenIsPerUser` | 同上 |
| `TestRegisterAndAuthenticate` / `TestCreateValidates` | 認證邊界 |
| `TestPingAndSweep` | 狀態轉換與冪等。壞了會重複寄信 |
| `TestEditRecomputesDue` | 改週期要立刻生效 |

沒有 handler 測試、沒有模板測試 —— 那些用瀏覽器走一遍更快也更真。

## 進度

| 里程碑 | 狀態 |
|---|---|
| M1 ping 端點 + 逾期偵測 | 已合併（PR #1） |
| M2 Email 通知與狀態轉換 | 已合併（PR #2） |
| M3 註冊登入與網頁介面 | 已合併（PR #3） |
| M4 開源發佈 | 進行中（`feat/m4-self-hosting`） |
| M5 託管版 | — |

分支：`main` ← `dev` ← feature 分支。PR 都開向 `dev`。

## 下一步（M4）

目標是**讓陌生人 clone 下來就能裝起來** —— PLAN.md 說這個模式的分發優勢靠這個成立。

1. **Dockerfile** —— 多階段建置，最後是單一 binary 的 scratch/distroless image
2. **docker-compose.yml 加上 app 本身** —— 現在只有 db 和 mailpit，
   clone 下來的人沒辦法一鍵啟動。這是 M4 最重要的一件
3. **`.env.example`** —— 目前設定散在 README 的表格裡
4. **部署文件** —— 至少一個 VPS 的完整步驟，含反向代理與 TLS
   （`Secure` cookie 需要 `X-Forwarded-Proto`，文件要寫到）
5. **README 的安裝段落**重寫成「五分鐘裝起來」

## 已知上限

寫在這裡，撞到才不會以為是 bug：

- **單一實例。** 偵測迴圈沒有分散式鎖，跑兩份會寄兩次通知。
  要水平擴充得用 `SELECT ... FOR UPDATE SKIP LOCKED` 或外部鎖
- **寄信沒有重試佇列。** 失敗只 log。要重試得先有一張 outbox 表
- **`pings` 靠時間裁切**（每小時砍 30 天前）。PLAN.md 原本寫「最近 N 筆」，
  改成時間是因為前者要 window function，後者是一句走索引的 DELETE
- **週期只支援固定間隔。** 不規則排程（工作日、月初）要等 cron 運算式
- **`checks.user_id` 可為 NULL。** M1/M2 期間的 demo check 沒有擁有者，
  它們在所有列表裡看不到也不寄信。不是 bug，是刻意讓 migration 不炸在既有資料上
- **htmx 從 CDN 載。** 離線環境會失去列表自動刷新，頁面本身照常運作。
  M4 要不要改成內嵌可以再議

## 慣例

- **commit 訊息寫「為什麼」，不是「改了什麼」** —— diff 已經說了改什麼
- 程式碼註解同理。`ponytail:` 開頭的註解標記刻意的簡化，並註明升級路徑
- 回覆與文件用繁體中文，程式碼與術語用英文
- 加依賴前先問：stdlib 能不能做？現有依賴能不能做？幾行能不能做？
